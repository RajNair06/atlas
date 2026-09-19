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

// fakeReplayer returns scripted responses, one per call.
// If err is set, every call fails with that error.
type fakeReplayer struct {
	statuses []int
	err      error
	calls    int
}

func (f *fakeReplayer) ReplayRequest(req *errors.FailedRequest) (*http.Response, error) {
	idx := f.calls
	f.calls++

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

func TestExecuteRetrySucceedsOnSecondAttempt(t *testing.T) {
	replayer := &fakeReplayer{statuses: []int{502, 200}}
	e := NewExecutor(nil, replayer, 3)

	start := time.Now()
	result, err := e.executeRetry(testFailedRequest(), retrySuggestion())
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

func TestExecuteRetryAllAttemptsFail(t *testing.T) {
	replayer := &fakeReplayer{statuses: []int{502, 502, 502}}
	e := NewExecutor(nil, replayer, 3)

	_, err := e.executeRetry(testFailedRequest(), retrySuggestion())

	if err == nil {
		t.Fatal("expected error after exhausting retries, got nil")
	}
	if replayer.calls != 3 {
		t.Fatalf("replayer called %d times, want 3", replayer.calls)
	}
}

func TestExecuteRetryConnectionErrors(t *testing.T) {
	replayer := &fakeReplayer{err: fmt.Errorf("connection refused")}
	e := NewExecutor(nil, replayer, 2)

	_, err := e.executeRetry(testFailedRequest(), retrySuggestion())

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if replayer.calls != 2 {
		t.Fatalf("replayer called %d times, want 2 (errors must not stop retries)", replayer.calls)
	}
}

func TestExecuteRetryRespectsMaxAttempts(t *testing.T) {
	replayer := &fakeReplayer{statuses: []int{502, 502, 502, 502, 502}}
	e := NewExecutor(nil, replayer, 2)

	_, err := e.executeRetry(testFailedRequest(), retrySuggestion())

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if replayer.calls != 2 {
		t.Fatalf("replayer called %d times, want 2 (maxAttempts must cap retries)", replayer.calls)
	}
}

func TestExecuteRetryFirstAttemptSuccess(t *testing.T) {
	replayer := &fakeReplayer{statuses: []int{200}}
	e := NewExecutor(nil, replayer, 3)

	result, err := e.executeRetry(testFailedRequest(), retrySuggestion())

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
