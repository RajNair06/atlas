package approval

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RajNair06/atlas/gateway/healing"
)

// testPending builds a PendingDecision with a unique ID and a creation time
// backdated by age so ordering and wait-time math are deterministic.
func testPending(id string, age time.Duration) healing.PendingDecision {
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
		CreatedAt:    time.Now().Add(-age),
	}
}

// await runs AwaitApproval in a goroutine and returns channels for its
// results so tests can synchronize without sleeping longer than needed.
func await(s *Store, pending healing.PendingDecision, timeout time.Duration) (<-chan healing.Decision, <-chan error) {
	decisionCh := make(chan healing.Decision, 1)
	errCh := make(chan error, 1)
	go func() {
		d, err := s.AwaitApproval(pending, timeout)
		decisionCh <- d
		errCh <- err
	}()
	return decisionCh, errCh
}

// waitForPending polls ListPending until the given decision ID is registered
// (or fails the test). AwaitApproval registers synchronously inside its
// goroutine, so this returns almost immediately.
func waitForPending(t *testing.T, s *Store, id string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, p := range s.ListPending() {
			if p.ID == id {
				return
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for pending decision %s", id)
}

func TestDecideApprovedUnblocksWait(t *testing.T) {
	s := NewStore()
	pending := testPending("a1", 0)

	decisionCh, errCh := await(s, pending, 5*time.Second)
	waitForPending(t, s, "a1")

	if err := s.Decide("a1", true, ""); err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("AwaitApproval error = %v, want nil on approval", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AwaitApproval did not unblock after approval")
	}
	decision := <-decisionCh
	if !decision.Approved {
		t.Fatal("decision.Approved = false, want true")
	}
	if got := len(s.ListPending()); got != 0 {
		t.Fatalf("pending count = %d, want 0 after decision", got)
	}
}

func TestDecideRejectedReturnsOperatorError(t *testing.T) {
	s := NewStore()
	pending := testPending("r1", 0)

	_, errCh := await(s, pending, 5*time.Second)
	waitForPending(t, s, "r1")

	if err := s.Decide("r1", false, "too risky right now"); err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected rejection error, got nil")
		}
		if want := "rejected by operator: too risky right now"; err.Error() != want {
			t.Fatalf("error = %q, want %q", err, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AwaitApproval did not unblock after rejection")
	}

	history := s.ListHistory()
	if len(history) != 1 {
		t.Fatalf("history len = %d, want 1", len(history))
	}
	if history[0].Outcome != healing.OutcomeRejected {
		t.Fatalf("outcome = %s, want rejected", history[0].Outcome)
	}
	if history[0].Reason != "too risky right now" {
		t.Fatalf("reason = %q", history[0].Reason)
	}
}

func TestRejectWithEmptyReasonGetsDefault(t *testing.T) {
	s := NewStore()
	_, errCh := await(s, testPending("r2", 0), 5*time.Second)
	waitForPending(t, s, "r2")

	_ = s.Decide("r2", false, "")

	err := <-errCh
	if !strings.Contains(err.Error(), "no reason given") {
		t.Fatalf("error = %q, want default reason", err)
	}
}

func TestTimeoutExpiresDecision(t *testing.T) {
	s := NewStore()
	pending := testPending("t1", 0)

	start := time.Now()
	_, errCh := await(s, pending, 50*time.Millisecond)

	select {
	case err := <-errCh:
		elapsed := time.Since(start)
		if err == nil {
			t.Fatal("expected timeout error, got nil")
		}
		if want := "approval timed out after 50ms"; err.Error() != want {
			t.Fatalf("error = %q, want %q", err, want)
		}
		if elapsed < 45*time.Millisecond {
			t.Fatalf("returned after %s — did not actually wait", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AwaitApproval never returned after timeout")
	}

	if got := len(s.ListPending()); got != 0 {
		t.Fatalf("pending count = %d, want 0 after expiry", got)
	}
	history := s.ListHistory()
	if len(history) != 1 || history[0].Outcome != healing.OutcomeExpired {
		t.Fatalf("history = %+v, want one expired entry", history)
	}
}

func TestDecideUnknownOrAlreadyDecidedIsNotFound(t *testing.T) {
	s := NewStore()

	if err := s.Decide("nope", true, ""); err != ErrNotFound {
		t.Fatalf("unknown id error = %v, want ErrNotFound", err)
	}

	_, errCh := await(s, testPending("d1", 0), 5*time.Second)
	waitForPending(t, s, "d1")
	if err := s.Decide("d1", true, ""); err != nil {
		t.Fatalf("first Decide: %v", err)
	}
	<-errCh
	if err := s.Decide("d1", true, ""); err != ErrNotFound {
		t.Fatalf("second Decide error = %v, want ErrNotFound", err)
	}
}

func TestConcurrentDecideSingleWinner(t *testing.T) {
	s := NewStore()
	_, errCh := await(s, testPending("c1", 0), 5*time.Second)
	waitForPending(t, s, "c1")

	const contenders = 25
	errs := make(chan error, contenders)
	var wg sync.WaitGroup
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- s.Decide("c1", true, fmt.Sprintf("winner %d", i))
		}(i)
	}
	wg.Wait()
	close(errs)

	wins := 0
	for err := range errs {
		if err == nil {
			wins++
		} else if err != ErrNotFound {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("winners = %d, want exactly 1", wins)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("AwaitApproval error = %v, want nil", err)
	}
}

func TestRecordOutcomeCompletesHistory(t *testing.T) {
	s := NewStore()
	pending := testPending("o1", 0)

	_, errCh := await(s, pending, 5*time.Second)
	waitForPending(t, s, "o1")
	if err := s.Decide("o1", true, ""); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("AwaitApproval: %v", err)
	}

	// Before the outcome is recorded there is no history entry yet —
	// the decision is "in flight", not decided-and-finished.
	if got := len(s.ListHistory()); got != 0 {
		t.Fatalf("history len = %d, want 0 while execution is in flight", got)
	}

	s.RecordOutcome("o1", healing.OutcomeHealed, 2, 430*time.Millisecond)

	history := s.ListHistory()
	if len(history) != 1 {
		t.Fatalf("history len = %d, want 1", len(history))
	}
	entry := history[0]
	if entry.Outcome != healing.OutcomeHealed {
		t.Fatalf("outcome = %s, want healed", entry.Outcome)
	}
	if entry.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", entry.Attempts)
	}
	if entry.ExecutionMs != 430 {
		t.Fatalf("execution_ms = %d, want 430", entry.ExecutionMs)
	}
	if entry.RequestID != pending.RequestID || entry.Action != healing.ActionRetry {
		t.Fatalf("entry lost pending context: %+v", entry)
	}
}

func TestRecordOutcomeUnknownIDIsIgnored(t *testing.T) {
	s := NewStore()
	s.RecordOutcome("ghost", healing.OutcomeFailed, 1, time.Second) // must not panic
	if got := len(s.ListHistory()); got != 0 {
		t.Fatalf("history len = %d, want 0", got)
	}
}

func TestHistoryNewestFirstAndCapped(t *testing.T) {
	s := NewStore()
	total := HistoryLimit + 10
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("h%03d", i)
		_, errCh := await(s, testPending(id, 0), 5*time.Second)
		waitForPending(t, s, id)
		if err := s.Decide(id, false, "no"); err != nil {
			t.Fatalf("Decide %s: %v", id, err)
		}
		<-errCh
	}

	history := s.ListHistory()
	if len(history) != HistoryLimit {
		t.Fatalf("history len = %d, want cap %d", len(history), HistoryLimit)
	}
	// The newest rejection was h059 (total-1); the oldest surviving is h010.
	if history[0].ID != fmt.Sprintf("h%03d", total-1) {
		t.Fatalf("newest = %s, want h%03d", history[0].ID, total-1)
	}
	if history[len(history)-1].ID != fmt.Sprintf("h%03d", total-HistoryLimit) {
		t.Fatalf("oldest = %s, want h%03d", history[len(history)-1].ID, total-HistoryLimit)
	}
}

func TestListPendingSortedOldestFirst(t *testing.T) {
	s := NewStore()
	_, err1 := await(s, testPending("p-new", 0), 5*time.Second)
	waitForPending(t, s, "p-new")
	_, err2 := await(s, testPending("p-old", time.Hour), 5*time.Second)
	waitForPending(t, s, "p-old")
	_, err3 := await(s, testPending("p-mid", time.Minute), 5*time.Second)
	waitForPending(t, s, "p-mid")

	pending := s.ListPending()
	order := []string{pending[0].ID, pending[1].ID, pending[2].ID}
	want := []string{"p-old", "p-mid", "p-new"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}

	// Unblock the goroutines so the test ends cleanly.
	_ = s.Decide("p-new", false, "done")
	_ = s.Decide("p-old", false, "done")
	_ = s.Decide("p-mid", false, "done")
	<-err1
	<-err2
	<-err3
}

func TestEventsBroadcastToSubscribers(t *testing.T) {
	s := NewStore()
	events, unsubscribe := s.Subscribe()
	defer unsubscribe()

	pending := testPending("e1", 0)
	_, errCh := await(s, pending, 5*time.Second)
	waitForPending(t, s, "e1")

	// First event: pending, carrying the full decision payload.
	select {
	case ev := <-events:
		if ev.Type != EventPending {
			t.Fatalf("event type = %s, want pending", ev.Type)
		}
		if ev.Pending == nil || ev.Pending.ID != "e1" {
			t.Fatalf("pending payload = %+v, want decision e1", ev.Pending)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no pending event received")
	}

	// Approve → decided event; RecordOutcome → decided event with the entry.
	if err := s.Decide("e1", true, ""); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	<-errCh
	select {
	case ev := <-events:
		if ev.Type != EventDecided {
			t.Fatalf("event type = %s, want decided", ev.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no decided event received")
	}

	s.RecordOutcome("e1", healing.OutcomeHealed, 1, 200*time.Millisecond)
	select {
	case ev := <-events:
		if ev.Type != EventDecided || ev.Entry == nil || ev.Entry.Outcome != healing.OutcomeHealed {
			t.Fatalf("event = %+v, want decided with healed entry", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no outcome event received")
	}
}

func TestSlowSubscriberDoesNotBlockBroadcast(t *testing.T) {
	s := NewStore()
	slow, unsubscribe := s.Subscribe()
	defer unsubscribe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// 100 expiries: far more than the subscriber buffer (32). The
		// publisher must never block on the full channel.
		for i := 0; i < 100; i++ {
			_, errCh := await(s, testPending(fmt.Sprintf("s%03d", i), 0), time.Millisecond)
			<-errCh
		}
	}()

	select {
	case <-done:
		// Publisher finished despite the subscriber never draining.
	case <-time.After(5 * time.Second):
		t.Fatal("broadcast blocked on a slow subscriber")
	}
	if len(slow) == 0 {
		t.Fatal("slow subscriber should still hold buffered events")
	}
}

func TestStatsReflectPendingAndHealed(t *testing.T) {
	s := NewStore()

	// One decision still pending (awaiting its verdict below).
	_, pendingErrCh := await(s, testPending("st-live", 0), 5*time.Second)
	waitForPending(t, s, "st-live")

	// Two healed executions...
	approveThroughStore(t, s, "st1", 1, 300*time.Millisecond)
	approveThroughStore(t, s, "st2", 3, 500*time.Millisecond)
	// ...one rejection and one expiry.
	rejectThroughStore(t, s, "st3")
	expireThroughStore(t, s, "st4")

	stats := s.Stats()
	if stats.Pending != 1 {
		t.Fatalf("pending = %d, want 1", stats.Pending)
	}
	if stats.Healed != 2 {
		t.Fatalf("healed = %d, want 2", stats.Healed)
	}
	if stats.AvgHealMs != 400 {
		t.Fatalf("avg_heal_ms = %d, want 400 (mean of 300 and 500)", stats.AvgHealMs)
	}

	// Unblock the live decision so the test ends cleanly.
	if err := s.Decide("st-live", false, "done"); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	<-pendingErrCh
}

// mustApprove approves a pending decision and drains the waiter.
func mustApprove(t *testing.T, s *Store, id string, errCh <-chan error) {
	t.Helper()
	if err := s.Decide(id, true, ""); err != nil {
		t.Fatalf("Decide %s: %v", id, err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("AwaitApproval %s: %v", id, err)
	}
}

// approveThroughStore drives one full approve → record-outcome cycle.
func approveThroughStore(t *testing.T, s *Store, id string, attempts int, execution time.Duration) {
	t.Helper()
	_, errCh := await(s, testPending(id, 0), 5*time.Second)
	waitForPending(t, s, id)
	mustApprove(t, s, id, errCh)
	s.RecordOutcome(id, healing.OutcomeHealed, attempts, execution)
}

// rejectThroughStore drives one full reject cycle synchronously.
func rejectThroughStore(t *testing.T, s *Store, id string) {
	t.Helper()
	_, errCh := await(s, testPending(id, 0), 5*time.Second)
	waitForPending(t, s, id)
	if err := s.Decide(id, false, "nope"); err != nil {
		t.Fatalf("Decide %s: %v", id, err)
	}
	<-errCh
}

// expireThroughStore drives one full expiry cycle synchronously.
func expireThroughStore(t *testing.T, s *Store, id string) {
	t.Helper()
	_, errCh := await(s, testPending(id, 0), time.Millisecond)
	<-errCh
}
