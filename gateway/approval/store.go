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
)

// HistoryEntry is one decided healing decision, kept for the console feed.
type HistoryEntry struct {
	ID          string                `json:"id"`
	RequestID   string                `json:"request_id"`
	Method      string                `json:"method"`
	Path        string                `json:"path"`
	Upstream    string                `json:"upstream"`
	StatusCode  int                   `json:"status_code"`
	Action      healing.HealingAction `json:"action"`
	Outcome     healing.Outcome       `json:"outcome"`
	Attempts    int                   `json:"attempts"`
	Reason      string                `json:"reason,omitempty"`
	CreatedAt   time.Time             `json:"created_at"`
	DecidedAt   time.Time             `json:"decided_at"`
	WaitMs      int64                 `json:"wait_ms"`
	ExecutionMs int64                 `json:"execution_ms"`
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
// The zero value is not usable; call NewStore.
type Store struct {
	mu sync.Mutex
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

// NewStore creates an empty approval store.
func NewStore() *Store {
	return &Store{
		pending:     make(map[string]*waiter),
		inflight:    make(map[string]*HistoryEntry),
		subscribers: make(map[chan Event]struct{}),
	}
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

// baseEntry builds the history skeleton shared by every outcome.
func baseEntry(pending healing.PendingDecision, decidedAt time.Time) HistoryEntry {
	if decidedAt.IsZero() {
		decidedAt = time.Now()
	}
	return HistoryEntry{
		ID:         pending.ID,
		RequestID:  pending.RequestID,
		Method:     pending.Method,
		Path:       pending.Path,
		Upstream:   pending.Upstream,
		StatusCode: pending.StatusCode,
		Action:     pending.Action,
		CreatedAt:  pending.CreatedAt,
		DecidedAt:  decidedAt,
		WaitMs:     decidedAt.Sub(pending.CreatedAt).Milliseconds(),
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
