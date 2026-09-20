package healing

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/RajNair06/atlas/gateway/errors"
)

// fakeReplayer returns scripted responses, one per call, and records what it
// was asked to replay. If err is set, every call fails with that error.
type fakeReplayer struct {
	statuses     []int
	err          error
	calls        int
	lastUpstream string
}

func (f *fakeReplayer) ReplayRequest(req *errors.FailedRequest) (*http.Response, error) {
	idx := f.calls
	f.calls++
	f.lastUpstream = req.Upstream

	if f.err != nil {
		return nil, f.err
	}

	status := http.StatusBadGateway
	if idx < len(f.statuses) {
		status = f.statuses[idx]
	}

	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"status":"charged"}`)),
	}, nil
}

// fakeAnalyzer returns a canned suggestion without calling any LLM.
// delay simulates a slow LLM for budget-timing tests.
type fakeAnalyzer struct {
	suggestion *HealingSuggestion
	err        error
	delay      time.Duration
	calls      int
}

func (f *fakeAnalyzer) AnalyzeError(req *errors.FailedRequest) (*HealingSuggestion, error) {
	f.calls++
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	return f.suggestion, f.err
}

func testFailedRequest() *errors.FailedRequest {
	return &errors.FailedRequest{
		RequestID:  "test-123",
		Method:     "GET",
		Path:       "/buy",
		Upstream:   "http://localhost:8081/buy",
		StatusCode: http.StatusBadGateway,
	}
}

func retrySuggestion() *HealingSuggestion {
	return &HealingSuggestion{Action: ActionRetry, Reasoning: "test suggestion"}
}

func generousOptions() ExecutorOptions {
	return ExecutorOptions{MaxAttempts: 3, LatencyBudget: time.Minute}
}

func farDeadline() time.Time {
	return time.Now().Add(time.Minute)
}

func TestExecuteRetrySucceedsOnSecondAttempt(t *testing.T) {
	replayer := &fakeReplayer{statuses: []int{502, 200}}
	e := NewExecutor(nil, replayer, generousOptions())

	start := time.Now()
	result, err := e.executeRetry(testFailedRequest(), retrySuggestion(), farDeadline(), new(int))
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", result.StatusCode)
	}
	if result.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", result.Attempts)
	}
	if replayer.calls != 2 {
		t.Fatalf("replayer called %d times, want 2", replayer.calls)
	}
	if string(result.Body) != `{"status":"charged"}` {
		t.Fatalf("body = %q", result.Body)
	}
	// backoff: 100ms before attempt 1 + 200ms before attempt 2
	if elapsed < 300*time.Millisecond {
		t.Fatalf("completed in %s — exponential backoff was not applied", elapsed)
	}
}

func TestExecuteRetryUsesCustomBaseDelay(t *testing.T) {
	replayer := &fakeReplayer{statuses: []int{502, 200}}
	e := NewExecutor(nil, replayer, ExecutorOptions{
		MaxAttempts:    3,
		LatencyBudget:  time.Minute,
		RetryBaseDelay: time.Millisecond, // shrink the 100ms default for fast tests
	})

	start := time.Now()
	result, err := e.executeRetry(testFailedRequest(), retrySuggestion(), farDeadline(), new(int))
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", result.Attempts)
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("took %s — RetryBaseDelay override was not applied", elapsed)
	}
}

func TestExecuteRetryAllAttemptsFail(t *testing.T) {
	replayer := &fakeReplayer{statuses: []int{502, 502, 502}}
	e := NewExecutor(nil, replayer, generousOptions())

	_, err := e.executeRetry(testFailedRequest(), retrySuggestion(), farDeadline(), new(int))

	if err == nil {
		t.Fatal("expected error after exhausting retries, got nil")
	}
	if replayer.calls != 3 {
		t.Fatalf("replayer called %d times, want 3", replayer.calls)
	}
}

func TestExecuteRetryConnectionErrors(t *testing.T) {
	replayer := &fakeReplayer{err: fmt.Errorf("connection refused")}
	e := NewExecutor(nil, replayer, ExecutorOptions{MaxAttempts: 2, LatencyBudget: time.Minute})

	_, err := e.executeRetry(testFailedRequest(), retrySuggestion(), farDeadline(), new(int))

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if replayer.calls != 2 {
		t.Fatalf("replayer called %d times, want 2 (errors must not stop retries)", replayer.calls)
	}
}

func TestExecuteRetryRespectsMaxAttempts(t *testing.T) {
	replayer := &fakeReplayer{statuses: []int{502, 502, 502, 502, 502}}
	e := NewExecutor(nil, replayer, ExecutorOptions{MaxAttempts: 2, LatencyBudget: time.Minute})

	_, err := e.executeRetry(testFailedRequest(), retrySuggestion(), farDeadline(), new(int))

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if replayer.calls != 2 {
		t.Fatalf("replayer called %d times, want 2 (maxAttempts must cap retries)", replayer.calls)
	}
}

func TestExecuteRetryFirstAttemptSuccess(t *testing.T) {
	replayer := &fakeReplayer{statuses: []int{200}}
	e := NewExecutor(nil, replayer, generousOptions())

	result, err := e.executeRetry(testFailedRequest(), retrySuggestion(), farDeadline(), new(int))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", result.Attempts)
	}
	if replayer.calls != 1 {
		t.Fatalf("replayer called %d times, want 1 (must stop at first success)", replayer.calls)
	}
}

func TestExecuteRetryStopsAtLatencyBudget(t *testing.T) {
	replayer := &fakeReplayer{statuses: []int{502, 502, 502}}
	e := NewExecutor(nil, replayer, generousOptions())

	// Budget of 150ms: attempt 1 sleeps 100ms and fails; attempt 2's budget
	// check lands at ~100ms... still inside. Tighten to 50ms so attempt 2
	// (checked at ~100ms) is over budget.
	_, err := e.executeRetry(testFailedRequest(), retrySuggestion(), time.Now().Add(50*time.Millisecond), new(int))

	if err == nil {
		t.Fatal("expected budget error, got nil")
	}
	if !strings.Contains(err.Error(), "budget") {
		t.Fatalf("error = %q, want it to mention the budget", err)
	}
	if replayer.calls != 1 {
		t.Fatalf("replayer called %d times, want 1 (budget must cut retries short)", replayer.calls)
	}
}

func TestExecuteFallbackSuccess(t *testing.T) {
	replayer := &fakeReplayer{statuses: []int{200}}
	e := NewExecutor(nil, replayer, generousOptions())

	req := testFailedRequest()
	req.Fallback = "http://backup:8084"

	result, err := e.executeFallback(req, retrySuggestion(), farDeadline(), new(int))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != ActionFallback {
		t.Fatalf("action = %s, want fallback", result.Action)
	}
	if replayer.lastUpstream != "http://backup:8084/buy" {
		t.Fatalf("fallback upstream = %q, want fallback base + original path", replayer.lastUpstream)
	}
}

func TestExecuteFallbackNotConfigured(t *testing.T) {
	replayer := &fakeReplayer{statuses: []int{200}}
	e := NewExecutor(nil, replayer, generousOptions())

	req := testFailedRequest() // Fallback is empty

	_, err := e.executeFallback(req, retrySuggestion(), farDeadline(), new(int))

	if err == nil {
		t.Fatal("expected error when no fallback configured, got nil")
	}
	if replayer.calls != 0 {
		t.Fatal("must not replay anything when fallback is missing")
	}
}

func TestExecuteFallbackUpstreamFails(t *testing.T) {
	replayer := &fakeReplayer{statuses: []int{502}}
	e := NewExecutor(nil, replayer, generousOptions())

	req := testFailedRequest()
	req.Fallback = "http://backup:8084"

	_, err := e.executeFallback(req, retrySuggestion(), farDeadline(), new(int))

	if err == nil {
		t.Fatal("expected error when fallback also fails, got nil")
	}
}

func TestExecuteHealingRetryExhaustedFallsBack(t *testing.T) {
	// 3 retry failures, then the fallback call succeeds
	replayer := &fakeReplayer{statuses: []int{502, 502, 502, 200}}
	analyzer := &fakeAnalyzer{suggestion: retrySuggestion()}
	e := NewExecutor(analyzer, replayer, generousOptions())

	req := testFailedRequest()
	req.Fallback = "http://backup:8084"

	result, err := e.ExecuteHealing(req)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != ActionFallback {
		t.Fatalf("action = %s, want fallback (last resort after retries)", result.Action)
	}
	if replayer.calls != 4 {
		t.Fatalf("replayer called %d times, want 4 (3 retries + 1 fallback)", replayer.calls)
	}
	if replayer.lastUpstream != "http://backup:8084/buy" {
		t.Fatalf("last upstream = %q, want the fallback URL", replayer.lastUpstream)
	}
}

func TestExecuteHealingGiveUp(t *testing.T) {
	replayer := &fakeReplayer{statuses: []int{200}}
	analyzer := &fakeAnalyzer{suggestion: &HealingSuggestion{Action: ActionGiveUp, Reasoning: "404 is permanent"}}
	e := NewExecutor(analyzer, replayer, generousOptions())

	_, err := e.ExecuteHealing(testFailedRequest())

	if err == nil {
		t.Fatal("expected error for give_up, got nil")
	}
	if !strings.Contains(err.Error(), "give_up") {
		t.Fatalf("error = %q, want it to mention give_up", err)
	}
	if replayer.calls != 0 {
		t.Fatal("give_up must not replay anything")
	}
}

func TestExecuteHealingCircuitOpensAndFailsFast(t *testing.T) {
	replayer := &fakeReplayer{err: fmt.Errorf("connection refused")}
	analyzer := &fakeAnalyzer{suggestion: retrySuggestion()}
	e := NewExecutor(analyzer, replayer, ExecutorOptions{
		MaxAttempts:      1,
		LatencyBudget:    time.Minute,
		BreakerThreshold: 2,
		BreakerReset:     time.Second,
	})

	req := testFailedRequest()

	// Two failures → breaker trips
	for i := 0; i < 2; i++ {
		if _, err := e.ExecuteHealing(req); err == nil {
			t.Fatalf("iteration %d: expected failure", i)
		}
	}
	analyzerCallsBefore := analyzer.calls

	// Third call: circuit is open → fast-fail, analyzer must NOT be called
	start := time.Now()
	_, err := e.ExecuteHealing(req)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected circuit-open error, got nil")
	}
	if !strings.Contains(err.Error(), "circuit open") {
		t.Fatalf("error = %q, want it to mention circuit open", err)
	}
	if analyzer.calls != analyzerCallsBefore {
		t.Fatal("open circuit must skip the LLM call entirely")
	}
	if elapsed > 50*time.Millisecond {
		t.Fatalf("fast-fail took %s — should be immediate", elapsed)
	}
}

func TestExecuteHealingSuccessResetsBreaker(t *testing.T) {
	// Each cycle: attempt 1 fails (502), attempt 2 succeeds (200) → 2 calls per cycle
	replayer := &fakeReplayer{statuses: []int{502, 200, 502, 200, 502, 200, 502, 200}}
	analyzer := &fakeAnalyzer{suggestion: retrySuggestion()}
	e := NewExecutor(analyzer, replayer, ExecutorOptions{
		MaxAttempts:      2,
		LatencyBudget:    time.Minute,
		BreakerThreshold: 3,
		BreakerReset:     time.Second,
	})

	req := testFailedRequest()

	// Each ExecuteHealing records 1 failure at entry, then a success on heal.
	// Four healed cycles must never trip a threshold-3 breaker.
	for i := 0; i < 4; i++ {
		if _, err := e.ExecuteHealing(req); err != nil {
			t.Fatalf("iteration %d: unexpected error: %v", i, err)
		}
	}

	breaker := e.breakers.For(req.Upstream)
	if breaker.State() != StateClosed {
		t.Fatalf("state = %s, want closed (successes reset the count)", breaker.State())
	}
}
