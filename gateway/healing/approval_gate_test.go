package healing

import (
	"strings"
	"testing"
	"time"
)

// recordedOutcome captures one RecordOutcome call for assertions.
type recordedOutcome struct {
	id        string
	outcome   Outcome
	attempts  int
	execution time.Duration
}

// fakeApprover scripts the approval gate: it records every PendingDecision it
// was asked about and returns a canned verdict (or error) from AwaitApproval.
// wait simulates a human taking time to decide. disabled simulates the
// console's runtime toggle switched off (zero value = gate armed).
type fakeApprover struct {
	decision Decision
	err      error
	wait     time.Duration
	disabled bool

	pendings []PendingDecision
	outcomes []recordedOutcome
}

func (f *fakeApprover) Enabled() bool { return !f.disabled }

func (f *fakeApprover) AwaitApproval(pending PendingDecision, timeout time.Duration) (Decision, error) {
	if f.wait > 0 {
		time.Sleep(f.wait)
	}
	f.pendings = append(f.pendings, pending)
	if f.err != nil {
		return Decision{}, f.err
	}
	return f.decision, nil
}

func (f *fakeApprover) RecordOutcome(id string, outcome Outcome, attempts int, execution time.Duration) {
	f.outcomes = append(f.outcomes, recordedOutcome{id: id, outcome: outcome, attempts: attempts, execution: execution})
}

// approvingGate returns a fakeApprover that approves everything.
func approvingGate() *fakeApprover {
	return &fakeApprover{decision: Decision{Approved: true, DecidedAt: time.Now()}}
}

// gatedExecutor builds an executor with an approval gate and the given budget.
func gatedExecutor(analyzer Analyzer, replayer RequestReplayer, approver Approver, budget time.Duration) *Executor {
	return NewExecutor(analyzer, replayer, ExecutorOptions{
		MaxAttempts:     3,
		LatencyBudget:   budget,
		Approver:        approver,
		ApprovalTimeout: 5 * time.Second,
	})
}

func TestGateApprovedExecutesAction(t *testing.T) {
	replayer := &fakeReplayer{statuses: []int{502, 200}}
	analyzer := &fakeAnalyzer{suggestion: retrySuggestion()}
	approver := approvingGate()
	e := gatedExecutor(analyzer, replayer, approver, time.Minute)

	req := testFailedRequest()
	req.ErrorBody = "payment unreachable: connection refused"

	result, err := e.ExecuteHealing(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", result.StatusCode)
	}
	if replayer.calls != 2 {
		t.Fatalf("replayer called %d times, want 2 (502 then 200)", replayer.calls)
	}

	// The gate saw exactly one pending decision carrying request + suggestion context.
	if len(approver.pendings) != 1 {
		t.Fatalf("pendings = %d, want 1", len(approver.pendings))
	}
	pending := approver.pendings[0]
	if pending.ID == "" {
		t.Fatal("pending decision has no id")
	}
	if pending.RequestID != req.RequestID || pending.Method != req.Method || pending.Path != req.Path {
		t.Fatalf("pending lost request context: %+v", pending)
	}
	if pending.Upstream != req.Upstream || pending.StatusCode != 502 {
		t.Fatalf("pending lost upstream context: %+v", pending)
	}
	if pending.ErrorExcerpt != "payment unreachable: connection refused" {
		t.Fatalf("error excerpt = %q", pending.ErrorExcerpt)
	}
	if pending.Action != ActionRetry || pending.Reasoning != "test suggestion" {
		t.Fatalf("pending lost suggestion: %+v", pending)
	}
	if pending.CreatedAt.IsZero() {
		t.Fatal("pending has no creation timestamp")
	}

	// The healed outcome was recorded for the console history.
	if len(approver.outcomes) != 1 {
		t.Fatalf("outcomes = %d, want 1", len(approver.outcomes))
	}
	outcome := approver.outcomes[0]
	if outcome.id != pending.ID || outcome.outcome != OutcomeHealed || outcome.attempts != 2 {
		t.Fatalf("recorded outcome = %+v, want healed with 2 attempts for %s", outcome, pending.ID)
	}
}

func TestGateRejectedZeroReplays(t *testing.T) {
	replayer := &fakeReplayer{statuses: []int{200}}
	analyzer := &fakeAnalyzer{suggestion: retrySuggestion()}
	approver := &fakeApprover{err: errRejected("deploy freeze in effect")}
	e := gatedExecutor(analyzer, replayer, approver, time.Minute)

	_, err := e.ExecuteHealing(testFailedRequest())

	if err == nil {
		t.Fatal("expected rejection error, got nil")
	}
	if want := "rejected by operator: deploy freeze in effect"; err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
	if replayer.calls != 0 {
		t.Fatalf("replayer called %d times — rejection must not replay anything", replayer.calls)
	}
	if len(approver.outcomes) != 0 {
		t.Fatal("executor must not record an outcome for a rejected decision (the approver owns it)")
	}
}

func TestGateTimeoutZeroReplays(t *testing.T) {
	replayer := &fakeReplayer{statuses: []int{200}}
	analyzer := &fakeAnalyzer{suggestion: retrySuggestion()}
	approver := &fakeApprover{err: errTimedOut(50 * time.Millisecond)}
	e := gatedExecutor(analyzer, replayer, approver, time.Minute)

	_, err := e.ExecuteHealing(testFailedRequest())

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "approval timed out after 50ms") {
		t.Fatalf("error = %q, want it to mention the approval timeout", err)
	}
	if replayer.calls != 0 {
		t.Fatalf("replayer called %d times — expiry must not replay anything", replayer.calls)
	}
}

func TestGateGiveUpApprovedRecordsFailed(t *testing.T) {
	replayer := &fakeReplayer{statuses: []int{200}}
	analyzer := &fakeAnalyzer{suggestion: &HealingSuggestion{Action: ActionGiveUp, Reasoning: "404 is permanent"}}
	approver := approvingGate()
	e := gatedExecutor(analyzer, replayer, approver, time.Minute)

	_, err := e.ExecuteHealing(testFailedRequest())

	if err == nil || !strings.Contains(err.Error(), "give_up") {
		t.Fatalf("error = %v, want give_up error", err)
	}
	if replayer.calls != 0 {
		t.Fatal("give_up must not replay even when approved")
	}
	if len(approver.outcomes) != 1 || approver.outcomes[0].outcome != OutcomeFailed {
		t.Fatalf("outcomes = %+v, want one failed entry (approved but unhealable)", approver.outcomes)
	}
}

func TestGateFailureAfterApprovalRecordsFailedWithReplayCount(t *testing.T) {
	replayer := &fakeReplayer{statuses: []int{502, 502, 502}} // every retry fails
	analyzer := &fakeAnalyzer{suggestion: retrySuggestion()}
	approver := approvingGate()
	e := gatedExecutor(analyzer, replayer, approver, time.Minute)

	_, err := e.ExecuteHealing(testFailedRequest())
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if len(approver.outcomes) != 1 {
		t.Fatalf("outcomes = %d, want 1", len(approver.outcomes))
	}
	outcome := approver.outcomes[0]
	if outcome.outcome != OutcomeFailed {
		t.Fatalf("outcome = %s, want failed", outcome.outcome)
	}
	if outcome.attempts != 3 {
		t.Fatalf("attempts = %d, want 3 replays counted", outcome.attempts)
	}
}

func TestGateDeadlineStartsAfterApproval(t *testing.T) {
	// The human takes 100ms; the machine budget is only 80ms. If the deadline
	// were computed before the wait (old behavior), the retry would be over
	// budget on arrival. It must succeed: the budget starts at approval.
	replayer := &fakeReplayer{statuses: []int{200}}
	analyzer := &fakeAnalyzer{suggestion: retrySuggestion()}
	approver := &fakeApprover{decision: Decision{Approved: true}, wait: 100 * time.Millisecond}
	e := gatedExecutor(analyzer, replayer, approver, 80*time.Millisecond)

	result, err := e.ExecuteHealing(testFailedRequest())

	if err != nil {
		t.Fatalf("unexpected error — approval wait consumed the machine budget: %v", err)
	}
	if result.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", result.StatusCode)
	}
}

func TestNoGateDeadlineStillCoversAnalysis(t *testing.T) {
	// Without an approver the behavior must be byte-for-byte the old one:
	// the budget starts BEFORE analysis, so a slow analyzer eats it.
	replayer := &fakeReplayer{statuses: []int{200}}
	analyzer := &fakeAnalyzer{suggestion: retrySuggestion(), delay: 100 * time.Millisecond}
	e := NewExecutor(analyzer, replayer, ExecutorOptions{MaxAttempts: 3, LatencyBudget: 80 * time.Millisecond})

	_, err := e.ExecuteHealing(testFailedRequest())

	if err == nil {
		t.Fatal("expected budget error — ungated healing must include analysis time in the budget")
	}
	if !strings.Contains(err.Error(), "budget") {
		t.Fatalf("error = %q, want it to mention the budget", err)
	}
	if replayer.calls != 0 {
		t.Fatalf("replayer called %d times, want 0 (budget exhausted before retries)", replayer.calls)
	}
}

func TestNoGateNeverTouchesApprover(t *testing.T) {
	replayer := &fakeReplayer{statuses: []int{200}}
	analyzer := &fakeAnalyzer{suggestion: retrySuggestion()}
	e := NewExecutor(analyzer, replayer, generousOptions()) // Approver: nil

	result, err := e.ExecuteHealing(testFailedRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", result.StatusCode)
	}
}

func TestGateDisabledAtRuntimeHealsWithoutAsking(t *testing.T) {
	// The console toggled the gate off: an approver exists but Enabled() is
	// false. Healing must run exactly like the ungated path — no pending
	// decision published, no outcome recorded, no human wait.
	replayer := &fakeReplayer{statuses: []int{200}}
	analyzer := &fakeAnalyzer{suggestion: retrySuggestion()}
	approver := &fakeApprover{decision: Decision{Approved: true}, disabled: true}
	e := gatedExecutor(analyzer, replayer, approver, time.Minute)

	result, err := e.ExecuteHealing(testFailedRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", result.StatusCode)
	}
	if len(approver.pendings) != 0 {
		t.Fatalf("pendings = %d, want 0 — a disabled gate must not publish decisions", len(approver.pendings))
	}
	if len(approver.outcomes) != 0 {
		t.Fatalf("outcomes = %d, want 0 — a disabled gate must not record outcomes", len(approver.outcomes))
	}
}

// errRejected / errTimedOut mirror the exact errors the approval store
// produces, so executor tests pin the contract end to end.
func errRejected(reason string) error {
	return &gateError{msg: "rejected by operator: " + reason}
}

func errTimedOut(timeout time.Duration) error {
	return &gateError{msg: "approval timed out after " + timeout.String()}
}

type gateError struct{ msg string }

func (e *gateError) Error() string { return e.msg }

// sanity check that the fake satisfies the interface
var _ Approver = (*fakeApprover)(nil)
