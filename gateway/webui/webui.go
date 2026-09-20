// Package webui serves the healing console: a dark, live dashboard where
// operators watch pending healing decisions and approve or reject them.
//
// It is a pure consumer of the approval store — healing and approval know
// nothing about this package (the dependency arrow points one way, so the
// UI can never wedge the healing pipeline). Routes live under /ui and are
// mounted on the gateway's existing mux; nothing else is touched.
package webui

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/RajNair06/atlas/gateway/approval"
	"github.com/RajNair06/atlas/gateway/healing"
)

// templates holds every console template (page shell + partials), rendered
// server-side. Partials are re-fetched over htmx when SSE events arrive.
//
//go:embed templates/*.html
var templates embed.FS

// sseHeartbeat keeps proxies and browsers from treating an idle stream as
// dead, and proves liveness for `curl -N /ui/events`.
const sseHeartbeat = 15 * time.Second

// Handler serves everything under /ui.
type Handler struct {
	store     *approval.Store
	autoHeal  bool
	templates *template.Template
	mux       *http.ServeMux
}

// NewHandler builds the console. Mount the result on the gateway mux at both
// "/ui" (exact) and "/ui/" (subtree). The approval-gate state is read live
// from the store on every render, so the console toggle and the healing
// pipeline can never disagree.
func NewHandler(store *approval.Store, autoHeal bool) *Handler {
	h := &Handler{
		store:     store,
		autoHeal:  autoHeal,
		templates: template.Must(template.ParseFS(templates, "templates/*.html")),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ui", h.handlePage)
	mux.HandleFunc("GET /ui/", h.handleUISubpath)
	mux.HandleFunc("GET /ui/events", h.handleEvents)
	mux.HandleFunc("GET /ui/decisions", h.handleDecisionsJSON)
	mux.HandleFunc("GET /ui/history", h.handleHistoryJSON)
	mux.HandleFunc("GET /ui/history/{id}", h.handleHistoryDetail)
	mux.HandleFunc("POST /ui/decisions/{id}/approve", h.handleDecide(true))
	mux.HandleFunc("POST /ui/decisions/{id}/reject", h.handleDecide(false))
	mux.HandleFunc("POST /ui/settings/approval", h.handleApprovalSettings)
	mux.HandleFunc("GET /ui/partials/pending", h.handlePendingPartial)
	mux.HandleFunc("GET /ui/partials/history", h.handleHistoryPartial)
	mux.HandleFunc("GET /ui/partials/stats", h.handleStatsPartial)
	mux.HandleFunc("GET /ui/partials/gate", h.handleGatePartial)
	h.mux = mux

	return h
}

// ServeHTTP delegates to the console's own router.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// handleUISubpath redirects the bare trailing-slash URL and 404s anything
// under /ui/ that is not a known console route.
func (h *Handler) handleUISubpath(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/ui/" {
		http.Redirect(w, r, "/ui", http.StatusFound)
		return
	}
	http.NotFound(w, r)
}

// pageData is everything the full-page render needs.
type pageData struct {
	AutoHeal bool
	Gate     gateView
	Pending  []pendingView
	History  []historyView
	Stats    statsView
}

// gateView renders the runtime approval-gate toggle. When auto_heal is off
// the switch renders disabled — there would be nothing to gate.
type gateView struct {
	Enabled  bool
	AutoHeal bool
}

// handlePage renders the whole dashboard server-side; SSE + htmx keep it
// live after load.
func (h *Handler) handlePage(w http.ResponseWriter, r *http.Request) {
	data := pageData{
		AutoHeal: h.autoHeal,
		Gate:     gateView{Enabled: h.store.Enabled(), AutoHeal: h.autoHeal},
		Pending:  h.pendingViews(),
		History:  h.historyViews(),
		Stats:    h.statsView(),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.templates.ExecuteTemplate(w, "page", data); err != nil {
		slog.Error("rendering console page", "error", err)
	}
}

// Partial handlers: htmx re-fetches these whenever an SSE event lands.

func (h *Handler) handlePendingPartial(w http.ResponseWriter, r *http.Request) {
	h.renderPartial(w, "pending-list", h.pendingViews())
}

func (h *Handler) handleHistoryPartial(w http.ResponseWriter, r *http.Request) {
	h.renderPartial(w, "history-feed", h.historyViews())
}

func (h *Handler) handleStatsPartial(w http.ResponseWriter, r *http.Request) {
	h.renderPartial(w, "stats", h.statsView())
}

func (h *Handler) handleGatePartial(w http.ResponseWriter, r *http.Request) {
	h.renderPartial(w, "gate-pill", gateView{Enabled: h.store.Enabled(), AutoHeal: h.autoHeal})
}

// handleApprovalSettings flips the runtime approval gate. Body: {"enabled":
// true|false}. The store broadcasts a "settings" SSE event, so every open
// console re-renders its toggle — including the one that clicked it.
func (h *Handler) handleApprovalSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil || body.Enabled == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": `body must be {"enabled": true|false}`})
		return
	}

	h.store.SetEnabled(*body.Enabled)
	slog.Info("approval gate toggled from console",
		"enabled", *body.Enabled,
		"remote_addr", r.RemoteAddr,
	)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "require_approval": *body.Enabled})
}

// detailView is the history detail modal: every field an operator needs to
// audit one decision after the fact.
type detailView struct {
	ID           string
	RequestID    string
	Method       string
	Path         string
	Upstream     string
	Fallback     string
	StatusCode   int
	StatusClass  string
	Action       string
	Reasoning    string
	Outcome      string
	Detail       string // tooltip on the outcome pill
	Attempts     string
	Wait         string
	Execution    string
	Created      string
	Decided      string
	Reason       string // operator's rejection reason, when present
	ErrorExcerpt string
}

// handleHistoryDetail renders the modal fragment for one history entry.
// htmx swaps it into #modal-root when a history row is clicked.
func (h *Handler) handleHistoryDetail(w http.ResponseWriter, r *http.Request) {
	entry, ok := h.store.History(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}

	detail := fmt.Sprintf("%s %s → %s (%d)", entry.Method, entry.Path, entry.Upstream, entry.StatusCode)
	if entry.Reason != "" {
		detail = entry.Reason + " · " + detail
	}

	view := detailView{
		ID:           entry.ID,
		RequestID:    entry.RequestID,
		Method:       entry.Method,
		Path:         entry.Path,
		Upstream:     entry.Upstream,
		Fallback:     entry.Fallback,
		StatusCode:   entry.StatusCode,
		StatusClass:  statusClass(entry.StatusCode),
		Action:       string(entry.Action),
		Reasoning:    entry.Reasoning,
		Outcome:      string(entry.Outcome),
		Detail:       detail,
		Wait:         formatDuration(entry.WaitMs),
		Created:      entry.CreatedAt.Local().Format("2006-01-02 15:04:05.000"),
		Decided:      entry.DecidedAt.Local().Format("2006-01-02 15:04:05.000"),
		Reason:       entry.Reason,
		ErrorExcerpt: entry.ErrorExcerpt,
	}
	switch entry.Outcome {
	case healing.OutcomeHealed, healing.OutcomeFailed:
		view.Attempts = fmt.Sprintf("%d %s", entry.Attempts, plural(entry.Attempts, "attempt"))
		view.Execution = formatDuration(entry.ExecutionMs)
	default:
		view.Attempts = "—"
		view.Execution = "—"
	}

	h.renderPartial(w, "history-detail", view)
}

func (h *Handler) renderPartial(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.templates.ExecuteTemplate(w, name, data); err != nil {
		slog.Error("rendering partial", "template", name, "error", err)
	}
}

// JSON endpoints — machine-readable views of the same store the UI renders.

func (h *Handler) handleDecisionsJSON(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.store.ListPending())
}

func (h *Handler) handleHistoryJSON(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.store.ListHistory())
}

// handleDecide builds the approve/reject POST handlers. Unknown or
// already-decided IDs are 404s, so a double-click or two operators racing
// produce exactly one verdict.
func (h *Handler) handleDecide(approved bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		reason := ""
		if !approved {
			// The rejection reason is an optional JSON body: {"reason": "..."}.
			// The console UI posts without one; API callers may include it.
			var body struct {
				Reason string `json:"reason"`
			}
			_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body)
			reason = strings.TrimSpace(body.Reason)
			if reason == "" {
				reason = "rejected via console"
			}
		}

		err := h.store.Decide(id, approved, reason)
		switch {
		case errors.Is(err, approval.ErrNotFound):
			writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "unknown or already decided"})
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		default:
			slog.Info("console decision submitted",
				"decision_id", id,
				"approved", approved,
				"remote_addr", r.RemoteAddr,
			)
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		}
	}
}

// handleEvents streams store events to the browser as Server-Sent Events.
// Each subscriber gets a buffered channel; slow ones are dropped by the
// store, so a stalled browser can never apply back-pressure to healing.
func (h *Handler) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // defeat intermediary buffering
	w.WriteHeader(http.StatusOK)

	// An immediate comment frame: clients (and curl) see the stream is live
	// without waiting for the first healing failure.
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	events, unsubscribe := h.store.Subscribe()
	defer unsubscribe()

	heartbeat := time.NewTicker(sseHeartbeat)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-events:
			data, err := json.Marshal(ev)
			if err != nil {
				slog.Error("encoding SSE event", "error", err)
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

// View models: templates stay dumb, all formatting happens here.

type pendingView struct {
	ID             string
	RequestID      string
	Method         string
	Path           string
	Upstream       string
	StatusCode     int
	StatusClass    string // badge tone: 5xx rose, 4xx amber
	ErrorExcerpt   string
	Action         string
	Reasoning      string
	Fallback       string
	CreatedRFC3339 string // drives the live "waiting Xs" timer in JS
}

type historyView struct {
	ID            string
	Time          string
	RequestID     string
	Action        string
	Outcome       string
	AttemptsLabel string
	Duration      string
	Detail        string // tooltip: full request + rejection reason
}

type statsView struct {
	Pending int
	Healed  int
	AvgHeal string
}

func (h *Handler) pendingViews() []pendingView {
	pending := h.store.ListPending()
	views := make([]pendingView, 0, len(pending))
	for _, p := range pending {
		views = append(views, pendingView{
			ID:             p.ID,
			RequestID:      p.RequestID,
			Method:         p.Method,
			Path:           p.Path,
			Upstream:       p.Upstream,
			StatusCode:     p.StatusCode,
			StatusClass:    statusClass(p.StatusCode),
			ErrorExcerpt:   p.ErrorExcerpt,
			Action:         string(p.Action),
			Reasoning:      p.Reasoning,
			Fallback:       p.Fallback,
			CreatedRFC3339: p.CreatedAt.Format(time.RFC3339Nano),
		})
	}
	return views
}

// statusClass maps an HTTP status to its badge tone: 5xx rose, 4xx amber,
// anything else neutral.
func statusClass(code int) string {
	switch {
	case code >= 500:
		return "badge-5xx"
	case code >= 400:
		return "badge-4xx"
	}
	return "badge-status"
}

func (h *Handler) historyViews() []historyView {
	history := h.store.ListHistory()
	views := make([]historyView, 0, len(history))
	for _, e := range history {
		detail := fmt.Sprintf("%s %s → %s (%d)", e.Method, e.Path, e.Upstream, e.StatusCode)
		if e.Reason != "" {
			detail = e.Reason + " · " + detail
		}

		view := historyView{
			ID:        e.ID,
			Time:      e.DecidedAt.Local().Format("15:04:05"),
			RequestID: e.RequestID,
			Action:    string(e.Action),
			Outcome:   string(e.Outcome),
			Detail:    detail,
		}
		switch e.Outcome {
		case healing.OutcomeHealed, healing.OutcomeFailed:
			// Execution stats: how many replays it took / how long they ran.
			view.AttemptsLabel = fmt.Sprintf("%d %s", e.Attempts, plural(e.Attempts, "attempt"))
			view.Duration = formatDuration(e.ExecutionMs)
		default:
			// Rejected/expired never executed; the interesting number is how
			// long the decision waited for a human.
			view.AttemptsLabel = "—"
			view.Duration = formatDuration(e.WaitMs)
		}
		views = append(views, view)
	}
	return views
}

func (h *Handler) statsView() statsView {
	stats := h.store.Stats()
	view := statsView{Pending: stats.Pending, Healed: stats.Healed, AvgHeal: "—"}
	if stats.Healed > 0 {
		view.AvgHeal = formatDuration(stats.AvgHealMs)
	}
	return view
}

// plural returns the bare word; callers print the count themselves
// ("1 attempt", "3 attempts").
func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// formatDuration renders milliseconds the way operators read them:
// sub-second precision when small, one decimal once it grows.
func formatDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encoding JSON response", "error", err)
	}
}
