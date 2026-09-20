package webui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RajNair06/atlas/gateway/approval"
	"github.com/RajNair06/atlas/gateway/healing"
)

func newTestHandler() (*Handler, *approval.Store) {
	store := approval.NewStore()
	return NewHandler(store, true), store
}

func testPending(id string) healing.PendingDecision {
	return healing.PendingDecision{
		ID:           id,
		RequestID:    "req-" + id,
		Method:       "POST",
		Path:         "/checkout",
		Upstream:     "http://localhost:8082/checkout",
		StatusCode:   502,
		ErrorExcerpt: "payment unreachable: connection refused",
		Action:       healing.ActionRetry,
		Reasoning:    "connection refused is usually transient",
		CreatedAt:    time.Now(),
	}
}

// startAwait runs AwaitApproval in the background and returns its error
// channel, waiting until the decision shows up in the store.
func startAwait(t *testing.T, store *approval.Store, pending healing.PendingDecision) <-chan error {
	t.Helper()
	errCh := make(chan error, 1)
	go func() {
		_, err := store.AwaitApproval(pending, 5*time.Second)
		errCh <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, p := range store.ListPending() {
			if p.ID == pending.ID {
				return errCh
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("decision %s never became pending", pending.ID)
	return nil
}

func do(h *Handler, method, path, body string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPageRenders(t *testing.T) {
	h, _ := newTestHandler()
	rec := do(h, http.MethodGet, "/ui", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content type = %q, want text/html", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"healing console",
		"Pending approvals",
		"Healing history",
		"/ui/events",  // SSE wiring
		"tailwindcss", // CDN stack
		"htmx",
		"auto_heal on",
		"approval gate on",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("page HTML missing %q", want)
		}
	}
}

func TestTrailingSlashRedirects(t *testing.T) {
	h, _ := newTestHandler()
	rec := do(h, http.MethodGet, "/ui/", "")
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/ui" {
		t.Fatalf("location = %q, want /ui", loc)
	}
}

func TestUnknownSubpathIs404(t *testing.T) {
	h, _ := newTestHandler()
	if rec := do(h, http.MethodGet, "/ui/nope", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestDecisionsJSONIsEmptyArray(t *testing.T) {
	h, _ := newTestHandler()
	rec := do(h, http.MethodGet, "/ui/decisions", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Fatalf("body = %q, want [] (array, not null)", got)
	}
}

func TestApproveUnknownIDIs404(t *testing.T) {
	h, _ := newTestHandler()
	rec := do(h, http.MethodPost, "/ui/decisions/deadbeef/approve", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if out["ok"] != false {
		t.Fatalf("body = %v, want ok:false", out)
	}
}

func TestApproveFlowAndHistoryJSON(t *testing.T) {
	h, store := newTestHandler()
	errCh := startAwait(t, store, testPending("web1"))

	// While pending, the JSON list and the pending partial show the decision.
	rec := do(h, http.MethodGet, "/ui/decisions", "")
	if !strings.Contains(rec.Body.String(), `"id":"web1"`) {
		t.Fatalf("decisions JSON missing web1: %s", rec.Body)
	}
	rec = do(h, http.MethodGet, "/ui/partials/pending", "")
	for _, want := range []string{"web1", "POST", "/checkout", "retry", "Approve", "Reject"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("pending partial missing %q", want)
		}
	}

	// Approve → 200 {"ok":true}, waiter unblocks without error.
	rec = do(h, http.MethodPost, "/ui/decisions/web1/approve", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("approve status = %d, want 200", rec.Code)
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["ok"] != true {
		t.Fatalf("approve body = %v, want ok:true", out)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("AwaitApproval error = %v, want nil", err)
	}

	// Second approve → 404 (already decided).
	if rec := do(h, http.MethodPost, "/ui/decisions/web1/approve", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("re-approve status = %d, want 404", rec.Code)
	}

	// Pending list is empty again; approved-but-unfinished has no history yet.
	if got := strings.TrimSpace(do(h, http.MethodGet, "/ui/decisions", "").Body.String()); got != "[]" {
		t.Fatalf("decisions = %q, want []", got)
	}
	if got := strings.TrimSpace(do(h, http.MethodGet, "/ui/history", "").Body.String()); got != "[]" {
		t.Fatalf("history = %q, want [] while execution is in flight", got)
	}

	// Record the healed outcome; it must surface in JSON and the partial.
	store.RecordOutcome("web1", healing.OutcomeHealed, 2, 430*time.Millisecond)

	rec = do(h, http.MethodGet, "/ui/history", "")
	var history []approval.HistoryEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &history); err != nil {
		t.Fatalf("history JSON: %v", err)
	}
	if len(history) != 1 || history[0].Outcome != healing.OutcomeHealed || history[0].Attempts != 2 {
		t.Fatalf("history = %+v, want one healed entry with 2 attempts", history)
	}

	rec = do(h, http.MethodGet, "/ui/partials/history", "")
	if !strings.Contains(rec.Body.String(), "healed") || !strings.Contains(rec.Body.String(), "req-web1") {
		t.Fatalf("history partial missing healed row: %s", rec.Body)
	}

	// Stats chip shows the healed count.
	rec = do(h, http.MethodGet, "/ui/partials/stats", "")
	if !strings.Contains(rec.Body.String(), ">1<") || !strings.Contains(rec.Body.String(), "healed") {
		t.Fatalf("stats partial missing healed count: %s", rec.Body)
	}
}

func TestRejectFlowWithReason(t *testing.T) {
	h, store := newTestHandler()
	errCh := startAwait(t, store, testPending("web2"))

	rec := do(h, http.MethodPost, "/ui/decisions/web2/reject", `{"reason":"deploy freeze"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("reject status = %d, want 200", rec.Code)
	}

	err := <-errCh
	if err == nil || !strings.Contains(err.Error(), "rejected by operator: deploy freeze") {
		t.Fatalf("AwaitApproval error = %v, want operator rejection with reason", err)
	}

	history := store.ListHistory()
	if len(history) != 1 || history[0].Outcome != healing.OutcomeRejected || history[0].Reason != "deploy freeze" {
		t.Fatalf("history = %+v, want one rejected entry with the reason", history)
	}
}

func TestSSEStreamSendsHeadersAndGreeting(t *testing.T) {
	h, store := newTestHandler()
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/ui/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("SSE request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type = %q, want text/event-stream", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
		t.Fatalf("cache control = %q, want no-cache", cc)
	}

	// The greeting comment must arrive without any healing activity.
	buf := make([]byte, 32)
	n, err := resp.Body.Read(buf)
	if err != nil && n == 0 {
		t.Fatalf("reading SSE greeting: %v", err)
	}
	if got := string(buf[:n]); !strings.Contains(got, ": connected") {
		t.Fatalf("first frame = %q, want ': connected'", got)
	}

	// A real event must also flow: create a pending decision while streaming.
	go func() {
		_, _ = store.AwaitApproval(testPending("sse1"), 2*time.Second)
	}()
	deadline := time.Now().Add(2 * time.Second)
	seen := false
	for time.Now().Before(deadline) && !seen {
		n, err := resp.Body.Read(buf)
		if n > 0 && strings.Contains(string(buf[:n]), "event: pending") {
			seen = true
		}
		if err != nil && n == 0 {
			break
		}
	}
	if !seen {
		t.Fatal("SSE stream never delivered the pending event")
	}
}

func TestApprovalToggleEndpoint(t *testing.T) {
	h, store := newTestHandler()

	// Gate defaults to armed.
	if !store.Enabled() {
		t.Fatal("store should default to enabled")
	}

	// Toggle off.
	rec := do(h, http.MethodPost, "/ui/settings/approval", `{"enabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("toggle status = %d, want 200", rec.Code)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("toggle body is not JSON: %v", err)
	}
	if out["ok"] != true || out["require_approval"] != false {
		t.Fatalf("toggle body = %v, want ok:true require_approval:false", out)
	}
	if store.Enabled() {
		t.Fatal("store still enabled after toggle off")
	}

	// The page renders the new state.
	if body := do(h, http.MethodGet, "/ui", "").Body.String(); !strings.Contains(body, "approval gate off") {
		t.Fatal("page should render the gate as off")
	}
	if body := do(h, http.MethodGet, "/ui", "").Body.String(); !strings.Contains(body, `aria-checked="false"`) {
		t.Fatal("toggle switch should render aria-checked=false")
	}

	// Malformed body → 400, state unchanged.
	if rec := do(h, http.MethodPost, "/ui/settings/approval", `{"nope":1}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad body status = %d, want 400", rec.Code)
	}
	if store.Enabled() {
		t.Fatal("bad request must not change the gate state")
	}

	// Toggle back on.
	rec = do(h, http.MethodPost, "/ui/settings/approval", `{"enabled":true}`)
	if rec.Code != http.StatusOK || !store.Enabled() {
		t.Fatal("toggle back on failed")
	}
}

func TestGatePartialReflectsStore(t *testing.T) {
	h, store := newTestHandler()

	rec := do(h, http.MethodGet, "/ui/partials/gate", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `aria-checked="true"`) {
		t.Fatalf("gate partial = %s, want aria-checked true", rec.Body.String())
	}

	store.SetEnabled(false)
	rec = do(h, http.MethodGet, "/ui/partials/gate", "")
	if !strings.Contains(rec.Body.String(), `aria-checked="false"`) || !strings.Contains(rec.Body.String(), "approval gate off") {
		t.Fatalf("gate partial after toggle = %s", rec.Body.String())
	}
}

func TestHistoryDetailModal(t *testing.T) {
	h, store := newTestHandler()

	// Drive one decision to rejection so history has an entry.
	errCh := startAwait(t, store, testPending("det1"))
	do(h, http.MethodPost, "/ui/decisions/det1/reject", `{"reason":"deploy freeze"}`)
	<-errCh

	rec := do(h, http.MethodGet, "/ui/history/det1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"healing decision",           // modal chrome
		"det1",                       // decision id
		"req-det1",                   // request id
		"gemini reasoning",           // reasoning section
		"connection refused is usually transient", // Gemini's reasoning text
		"operator reason",            // rejection section
		"deploy freeze",              // the rejection reason
		"payment unreachable",        // captured error excerpt
		"rejected",                   // outcome pill
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("detail modal missing %q\nbody: %s", want, body)
		}
	}

	// Unknown ID → 404.
	if rec := do(h, http.MethodGet, "/ui/history/nope", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id status = %d, want 404", rec.Code)
	}
}
