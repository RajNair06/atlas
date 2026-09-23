// Package approval implements the human-in-the-loop gate for healing:
// a thread-safe store of pending decisions that blocks the healing executor
// until an operator approves or rejects (or a timer expires the decision),
// plus a capped in-memory history and an event feed for the web console.
//
// It implements healing.Approver. The dependency points one way only:
// approval imports healing (for the shared vocabulary types); healing knows
// nothing about this package, the store, or the UI.
package approval

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RajNair06/atlas/gateway/healing"
)

// ErrNotFound is returned by Decide for unknown or already-decided IDs.
var ErrNotFound = errors.New("decision not found or already decided")

// HistoryLimit caps the in-memory history at the 50 most recent entries.
const HistoryLimit = 50

// subscriberBuffer sizes each SSE subscriber channel. Broadcasts never
// block: a subscriber whose buffer is full simply misses that event (the UI
// re-fetches whole lists on the next event, so a miss is self-correcting).
const subscriberBuffer = 32

// Event names pushed to UI subscribers.
const (
	EventPending = "pending" // a new decision is awaiting approval
	EventDecided = "decided" // a decision was approved/rejected/expired, or its outcome was recorded
	EventSettings = "settings" // the runtime approval-gate toggle changed
)

// HistoryEntry is one decided healing decision, kept for the console feed.
// It carries the full judgment context (Gemini's reasoning, the captured
// error, the configured fallback) so the detail modal can render everything
// an operator needs after the fact.
type HistoryEntry struct {
	ID           string                `json:"id"`
	RequestID    string                `json:"request_id"`
	Method       string                `json:"method"`
	Path         string                `json:"path"`
	Upstream     string                `json:"upstream"`
	Fallback     string                `json:"fallback,omitempty"`
	StatusCode   int                   `json:"status_code"`
	Action       healing.HealingAction `json:"action"`
	Reasoning    string                `json:"reasoning,omitempty"`
	Outcome      healing.Outcome       `json:"outcome"`
	Attempts     int                   `json:"attempts"`
	Reason       string                `json:"reason,omitempty"`
	ErrorExcerpt string                `json:"error_excerpt,omitempty"`
	CreatedAt    time.Time             `json:"created_at"`
	DecidedAt    time.Time             `json:"decided_at"`
	WaitMs       int64                 `json:"wait_ms"`
	ExecutionMs  int64                 `json:"execution_ms"`
}

// Stats are the live counters shown as chips in the console top bar.
type Stats struct {
	Pending   int   `json:"pending"`
	Healed    int   `json:"healed"`
	AvgHealMs int64 `json:"avg_heal_ms"`
}

// Event is one SSE push. Pending is set for "pending" events; Entry is set
// for "decided" events that completed a history row (rejections, expiries,
// recorded outcomes).
type Event struct {
	Type    string                   `json:"type"`
	Pending *healing.PendingDecision `json:"pending,omitempty"`
	Entry   *HistoryEntry            `json:"entry,omitempty"`
}

// waiter is one in-flight approval: the payload plus the channel the human
// verdict arrives on. The channel is buffered (cap 1) and has exactly one
// reader — the blocked AwaitApproval call — so Decide never blocks.
type waiter struct {
	pending   healing.PendingDecision
	decisions chan healing.Decision
}

// Store holds pending decisions, the decided history, and UI subscribers.
// It also owns the runtime approval-gate toggle: healing checks Enabled()
// per failure, and the console flips it live via SetEnabled.
// The zero value is not usable; call NewStore.
type Store struct {
	mu sync.Mutex
	// enabled arms the human-in-the-loop gate. Atomic because the executor
	// reads it on the hot path while the console writes it at any time.
	enabled atomic.Bool
	// pending maps decision ID → waiter for decisions still awaiting a human.
	pending map[string]*waiter
	// inflight maps decision ID → history skeleton for approved decisions
	// whose execution outcome has not been recorded yet.
	inflight map[string]*HistoryEntry
	// history holds decided entries, newest first, capped at HistoryLimit.
	history []HistoryEntry
	// subscribers receive every event; sends are non-blocking.
	subscribers map[chan Event]struct{}
}

// Compile-time proof that the store is what the executor asks for.
var _ healing.Approver = (*Store)(nil)
var _ healing.AutoRecorder = (*Store)(nil)

// NewStore creates an empty approval store with the gate ARMED (the safe
// default for a human-approval feature). main.go sets the initial state
// from server.require_approval; the console can toggle it at runtime.
func NewStore() *Store {
	s := &Store{
		pending:     make(map[string]*waiter),
		inflight:    make(map[string]*HistoryEntry),
		subscribers: make(map[chan Event]struct{}),
	}
	s.enabled.Store(true)
	return s
}

// Enabled implements healing.Approver: reports whether the gate is armed.
func (s *Store) Enabled() bool { return s.enabled.Load() }

// SetEnabled arms or disarms the gate at runtime and notifies subscribers
// so every open console updates its toggle. Unchanged values are no-ops
// (no event, no log noise).
func (s *Store) SetEnabled(on bool) {
	if s.enabled.Swap(on) == on {
		return
	}
	slog.Info("approval gate toggled", "enabled", on)
	s.broadcast(Event{Type: EventSettings})
}

// Subscribe registers a UI listener. The returned func unsubscribes; the
// channel is never closed, so consumers must always unsubscribe via it.
func (s *Store) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, subscriberBuffer)
	s.mu.Lock()
	s.subscribers[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.subscribers, ch)
		s.mu.Unlock()
	}
}

// broadcast pushes an event to every subscriber without ever blocking:
// a full channel drops the event (slow subscriber protection).
func (s *Store) broadcast(ev Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subscribers {
		select {
		case ch <- ev:
		default:
			slog.Warn("dropping event for slow subscriber", "type", ev.Type)
		}
	}
}

// AwaitApproval implements healing.Approver. It registers the pending
// decision, pushes a "pending" event to the UI, and blocks until a human
// decides or the timeout expires. Exactly one caller ever waits per
// decision ID, and Decide removes the entry before sending, so the
// timer/receipt races are resolved deterministically under the mutex.
func (s *Store) AwaitApproval(pending healing.PendingDecision, timeout time.Duration) (healing.Decision, error) {
	if pending.ID == "" {
		return healing.Decision{}, errors.New("pending decision requires an id")
	}
	if pending.CreatedAt.IsZero() {
		pending.CreatedAt = time.Now()
	}

	w := &waiter{pending: pending, decisions: make(chan healing.Decision, 1)}
	s.mu.Lock()
	s.pending[pending.ID] = w
	s.mu.Unlock()

	slog.Info("healing decision awaiting approval",
		"decision_id", pending.ID,
		"request_id", pending.RequestID,
		"action", pending.Action,
		"timeout", timeout.String(),
	)
	s.broadcast(Event{Type: EventPending, Pending: &pending})

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case decision := <-w.decisions:
		return s.settle(pending, decision)

	case <-timer.C:
		// Claim the entry under the lock. If it is still there, no human
		// decision arrived: expire it. If it is gone, Decide beat the timer
		// and a verdict is already queued on the buffered channel.
		s.mu.Lock()
		_, stillPending := s.pending[pending.ID]
		if stillPending {
			delete(s.pending, pending.ID)
		}
		s.mu.Unlock()

		if stillPending {
			now := time.Now()
			entry := s.record(pending, healing.OutcomeExpired, 0, "", now, 0)
			slog.Warn("healing decision expired without a verdict",
				"decision_id", pending.ID,
				"request_id", pending.RequestID,
				"timeout", timeout.String(),
			)
			s.broadcast(Event{Type: EventDecided, Entry: &entry})
			return healing.Decision{}, fmt.Errorf("approval timed out after %s", timeout)
		}
		decision := <-w.decisions
		return s.settle(pending, decision)
	}
}

// settle processes a human verdict. Approved decisions park a history
// skeleton in inflight (RecordOutcome completes it after execution);
// rejections go straight into the history as "rejected".
func (s *Store) settle(pending healing.PendingDecision, decision healing.Decision) (healing.Decision, error) {
	if decision.Approved {
		skeleton := baseEntry(pending, decision.DecidedAt)
		s.mu.Lock()
		s.inflight[pending.ID] = &skeleton
		s.mu.Unlock()

		slog.Info("healing decision approved by operator",
			"decision_id", pending.ID,
			"request_id", pending.RequestID,
			"action", pending.Action,
			"wait_ms", skeleton.WaitMs,
		)
		s.broadcast(Event{Type: EventDecided})
		return decision, nil
	}

	if decision.Reason == "" {
		decision.Reason = "no reason given"
	}
	entry := s.record(pending, healing.OutcomeRejected, 0, decision.Reason, decision.DecidedAt, 0)
	slog.Info("healing decision rejected by operator",
		"decision_id", pending.ID,
		"request_id", pending.RequestID,
		"reason", decision.Reason,
		"wait_ms", entry.WaitMs,
	)
	s.broadcast(Event{Type: EventDecided, Entry: &entry})
	return decision, fmt.Errorf("rejected by operator: %s", decision.Reason)
}

// Decide delivers a human verdict. It returns ErrNotFound for unknown or
// already-decided IDs, making concurrent approvals race-free: only the
// caller that removes the entry under the lock gets to send the verdict.
func (s *Store) Decide(id string, approved bool, reason string) error {
	s.mu.Lock()
	w, ok := s.pending[id]
	if ok {
		delete(s.pending, id)
	}
	s.mu.Unlock()

	if !ok {
		return ErrNotFound
	}
	// Buffered (cap 1) with exactly one reader: never blocks.
	w.decisions <- healing.Decision{Approved: approved, Reason: reason, DecidedAt: time.Now()}
	return nil
}

// RecordOutcome implements healing.Approver: it completes the history entry
// for an approved decision once the executor knows whether the heal worked.
func (s *Store) RecordOutcome(id string, outcome healing.Outcome, attempts int, execution time.Duration) {
	s.mu.Lock()
	entry, ok := s.inflight[id]
	if ok {
		delete(s.inflight, id)
		entry.Outcome = outcome
		entry.Attempts = attempts
		entry.ExecutionMs = execution.Milliseconds()
		s.prependHistory(*entry)
	}
	s.mu.Unlock()

	if !ok {
		slog.Warn("recorded outcome for unknown decision", "decision_id", id, "outcome", outcome)
		return
	}

	slog.Info("healing outcome recorded",
		"decision_id", id,
		"request_id", entry.RequestID,
		"outcome", outcome,
		"attempts", attempts,
		"execution_ms", entry.ExecutionMs,
	)
	done := *entry
	s.broadcast(Event{Type: EventDecided, Entry: &done})
}

// RecordAutoHeal implements healing.AutoRecorder: it adds an UNGATED healing
// outcome (approval gate off) to the same history feed and broadcasts it, so
// the console stays live and complete regardless of gate state. No human was
// involved, so WaitMs is 0 and the note marks it as automatic.
func (s *Store) RecordAutoHeal(outcome healing.AutoHealOutcome) {
	now := time.Now()
	pending := healing.PendingDecision{
		ID:           fmt.Sprintf("auto-%x", now.UnixNano()),
		RequestID:    outcome.RequestID,
		Method:       outcome.Method,
		Path:         outcome.Path,
		Upstream:     outcome.Upstream,
		Fallback:     outcome.Fallback,
		StatusCode:   outcome.StatusCode,
		ErrorExcerpt: outcome.ErrorExcerpt,
		Action:       outcome.Action,
		Reasoning:    outcome.Reasoning,
		CreatedAt:    now,
	}

	result := healing.OutcomeFailed
	if outcome.Healed {
		result = healing.OutcomeHealed
	}

	entry := baseEntry(pending, now)
	entry.Outcome = result
	entry.Attempts = outcome.Attempts
	entry.Reason = "auto-heal · approval gate off"
	entry.ExecutionMs = outcome.ExecutionMs

	s.mu.Lock()
	s.prependHistory(entry)
	s.mu.Unlock()

	slog.Info("auto-heal outcome recorded",
		"request_id", entry.RequestID,
		"action", entry.Action,
		"outcome", entry.Outcome,
		"attempts", outcome.Attempts,
		"execution_ms", entry.ExecutionMs,
	)
	s.broadcast(Event{Type: EventDecided, Entry: &entry})
}

// ListPending returns decisions awaiting approval, oldest first (stable
// ordering for the UI).
func (s *Store) ListPending() []healing.PendingDecision {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]healing.PendingDecision, 0, len(s.pending))
	for _, w := range s.pending {
		out = append(out, w.pending)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// ListHistory returns decided entries, newest first.
func (s *Store) ListHistory() []HistoryEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]HistoryEntry, len(s.history))
	copy(out, s.history)
	return out
}

// History returns one decided entry by decision ID — the lookup behind the
// console's detail modal. The history is capped at 50 entries, so a linear
// scan under the lock is both fast and lock-order-simple.
func (s *Store) History(id string) (HistoryEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range s.history {
		if entry.ID == id {
			return entry, true
		}
	}
	return HistoryEntry{}, false
}

// Stats computes the live console counters over pending + history.
func (s *Store) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()

	stats := Stats{Pending: len(s.pending)}
	var totalHealMs int64
	for _, entry := range s.history {
		if entry.Outcome == healing.OutcomeHealed {
			stats.Healed++
			totalHealMs += entry.ExecutionMs
		}
	}
	if stats.Healed > 0 {
		stats.AvgHealMs = totalHealMs / int64(stats.Healed)
	}
	return stats
}

// record appends a terminal (non-approved) outcome to the history and
// returns the stored entry.
func (s *Store) record(pending healing.PendingDecision, outcome healing.Outcome, attempts int, reason string, decidedAt time.Time, execution time.Duration) HistoryEntry {
	entry := baseEntry(pending, decidedAt)
	entry.Outcome = outcome
	entry.Attempts = attempts
	entry.Reason = reason
	entry.ExecutionMs = execution.Milliseconds()

	s.mu.Lock()
	s.prependHistory(entry)
	s.mu.Unlock()
	return entry
}

// baseEntry builds the history skeleton shared by every outcome. It carries
// the full judgment context from the pending decision so the detail modal
// can show Gemini's reasoning and the captured error after the fact.
func baseEntry(pending healing.PendingDecision, decidedAt time.Time) HistoryEntry {
	if decidedAt.IsZero() {
		decidedAt = time.Now()
	}
	return HistoryEntry{
		ID:           pending.ID,
		RequestID:    pending.RequestID,
		Method:       pending.Method,
		Path:         pending.Path,
		Upstream:     pending.Upstream,
		Fallback:     pending.Fallback,
		StatusCode:   pending.StatusCode,
		Action:       pending.Action,
		Reasoning:    pending.Reasoning,
		ErrorExcerpt: pending.ErrorExcerpt,
		CreatedAt:    pending.CreatedAt,
		DecidedAt:    decidedAt,
		WaitMs:       decidedAt.Sub(pending.CreatedAt).Milliseconds(),
	}
}

// prependHistory adds an entry newest-first and enforces the cap.
// Callers must hold s.mu.
func (s *Store) prependHistory(entry HistoryEntry) {
	s.history = append([]HistoryEntry{entry}, s.history...)
	if len(s.history) > HistoryLimit {
		s.history = s.history[:HistoryLimit]
	}
}
