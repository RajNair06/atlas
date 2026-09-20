package healing

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/RajNair06/atlas/gateway/errors"
)

// RequestReplayer re-sends a previously captured failed request.
// Implemented by the proxy Gateway. The interface lives here (where it is
// used) so that healing never imports proxy — that would be a cycle.
type RequestReplayer interface {
	ReplayRequest(req *errors.FailedRequest) (*http.Response, error)
}

// Analyzer produces a healing suggestion for a failed request.
// Implemented by DecisionEngine (Gemini); faked in tests.
type Analyzer interface {
	AnalyzeError(failedReq *errors.FailedRequest) (*HealingSuggestion, error)
}

// HealingResult carries the successful response produced by a healing action,
// plus metadata about how it was produced. The body is fully read so the
// caller owns plain bytes — no streams to close, no leaked connections.
type HealingResult struct {
	Action     HealingAction
	Reasoning  string
	Attempts   int
	StatusCode int
	Header     http.Header
	Body       []byte
}

// ExecutorOptions configures an Executor. Zero values get sensible defaults.
type ExecutorOptions struct {
	MaxAttempts      int           // retry attempts before falling through (default 3)
	LatencyBudget    time.Duration // hard cap on total healing time (default 5s)
	BreakerThreshold int           // consecutive failures that trip a breaker (<=0 disables)
	BreakerReset     time.Duration // how long a breaker stays open (default 30s)
	Approver         Approver      // optional human-in-the-loop gate (nil = heal without asking)
	ApprovalTimeout  time.Duration // how long a decision waits for a human (default 2m)
}

// Executor performs the healing actions suggested by the decision engine,
// guarded by a per-upstream circuit breaker and a per-request latency budget.
// When an Approver is set, every suggestion first waits for a human verdict.
type Executor struct {
	analyzer        Analyzer
	replayer        RequestReplayer
	maxAttempts     int
	latencyBudget   time.Duration
	breakers        *BreakerRegistry
	approver        Approver
	approvalTimeout time.Duration
}

// defaultApprovalTimeout bounds the human wait when none is configured.
// Two minutes: long enough for an operator to read and decide, short enough
// that a forgotten console tab does not pin requests forever.
const defaultApprovalTimeout = 2 * time.Minute

// NewExecutor creates a healing executor.
func NewExecutor(analyzer Analyzer, replayer RequestReplayer, opts ExecutorOptions) *Executor {
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 3
	}
	if opts.LatencyBudget <= 0 {
		opts.LatencyBudget = 5 * time.Second
	}
	if opts.BreakerReset <= 0 {
		opts.BreakerReset = 30 * time.Second
	}
	if opts.ApprovalTimeout <= 0 {
		opts.ApprovalTimeout = defaultApprovalTimeout
	}
	return &Executor{
		analyzer:        analyzer,
		replayer:        replayer,
		maxAttempts:     opts.MaxAttempts,
		latencyBudget:   opts.LatencyBudget,
		breakers:        NewBreakerRegistry(opts.BreakerThreshold, opts.BreakerReset),
		approver:        opts.Approver,
		approvalTimeout: opts.ApprovalTimeout,
	}
}

const retryBaseDelay = 100 * time.Millisecond

// ExecuteHealing is the full healing pipeline for one captured failure:
// breaker gate → analyze with the LLM → (optional) human approval gate →
// execute the suggested action → (retry only) fall through to the configured
// fallback as a last resort.
func (e *Executor) ExecuteHealing(failedReq *errors.FailedRequest) (*HealingResult, error) {
	breaker := e.breakers.For(failedReq.Upstream)
	if !breaker.Allow() {
		return nil, fmt.Errorf("circuit open for %s: failing fast, skipping LLM and retries", failedReq.Upstream)
	}
	// We are here because the upstream request failed — count it immediately.
	// A successful heal below will reset the count.
	breaker.RecordFailure()

	// The machine latency budget. Without the approval gate it starts now and
	// covers analysis + execution (the original behavior). With the gate it is
	// only computed after approval is granted — human think time must not eat
	// the machine budget.
	gated := e.approver != nil
	var deadline time.Time
	if !gated {
		deadline = time.Now().Add(e.latencyBudget)
	}

	suggestion, err := e.analyzer.AnalyzeError(failedReq)
	if err != nil {
		return nil, fmt.Errorf("analysis failed: %w", err)
	}

	slog.Info("healing suggestion received",
		"request_id", failedReq.RequestID,
		"action", suggestion.Action,
		"reasoning", suggestion.Reasoning,
	)

	// Human-in-the-loop gate: publish the suggestion and block until an
	// operator approves or rejects it (or the approval timeout expires).
	// Rejection and expiry are errors — the proxy returns the original failure.
	var pending PendingDecision
	if gated {
		pending = NewPendingDecision(failedReq, suggestion)
		waitStarted := time.Now()

		_, waitErr := e.approver.AwaitApproval(pending, e.approvalTimeout)
		if waitErr != nil {
			slog.Warn("healing stopped at approval gate",
				"request_id", failedReq.RequestID,
				"decision_id", pending.ID,
				"wait_ms", time.Since(waitStarted).Milliseconds(),
				"error", waitErr,
			)
			return nil, waitErr
		}

		slog.Info("healing approved by operator",
			"request_id", failedReq.RequestID,
			"decision_id", pending.ID,
			"wait_ms", time.Since(waitStarted).Milliseconds(),
		)
		deadline = time.Now().Add(e.latencyBudget)
	}

	var result *HealingResult
	replays := 0 // total replay calls made below, for the console history
	execStarted := time.Now()

	switch suggestion.Action {
	case ActionRetry:
		result, err = e.executeRetry(failedReq, suggestion, deadline, &replays)
		// Last resort: retries exhausted, a fallback is configured, budget remains.
		if err != nil && failedReq.Fallback != "" && time.Now().Before(deadline) {
			slog.Info("retries exhausted, trying configured fallback as last resort",
				"request_id", failedReq.RequestID,
				"fallback", failedReq.Fallback,
			)
			result, err = e.executeFallback(failedReq, suggestion, deadline, &replays)
		}
	case ActionFallback:
		result, err = e.executeFallback(failedReq, suggestion, deadline, &replays)
	case ActionGiveUp:
		err = fmt.Errorf("gemini recommended give_up: %s", suggestion.Reasoning)
	default:
		err = fmt.Errorf("unknown action: %s", suggestion.Action)
	}

	if gated {
		// Finalize the console history entry for this decision.
		outcome, attempts := OutcomeFailed, replays
		if err == nil {
			outcome, attempts = OutcomeHealed, result.Attempts
		}
		e.approver.RecordOutcome(pending.ID, outcome, attempts, time.Since(execStarted))
	}

	if err != nil {
		return nil, err
	}

	breaker.RecordSuccess()
	return result, nil
}

// executeRetry replays the request with exponential backoff: 100ms, 200ms, 400ms...
// It stops at the first success (status < 400), the latency budget, or maxAttempts.
// replays (may not be nil) counts every replay call for the console history.
func (e *Executor) executeRetry(failedReq *errors.FailedRequest, suggestion *HealingSuggestion, deadline time.Time, replays *int) (*HealingResult, error) {
	delay := retryBaseDelay

	for attempt := 1; attempt <= e.maxAttempts; attempt++ {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("healing latency budget exhausted after %d attempts", attempt-1)
		}

		slog.Info("healing retry attempt",
			"request_id", failedReq.RequestID,
			"attempt", attempt,
			"max_attempts", e.maxAttempts,
			"delay_ms", delay.Milliseconds(),
		)
		time.Sleep(delay)
		delay *= 2

		result, ok := e.replayOnce(failedReq, suggestion, attempt, ActionRetry, replays)
		if ok {
			return result, nil
		}
	}

	return nil, fmt.Errorf("all %d retry attempts failed", e.maxAttempts)
}

// executeFallback replays the request against the route's configured fallback
// URL (fallback base + original path). A single attempt; the caller decides
// whether it is a direct suggestion or a last resort after retries.
func (e *Executor) executeFallback(failedReq *errors.FailedRequest, suggestion *HealingSuggestion, deadline time.Time, replays *int) (*HealingResult, error) {
	if failedReq.Fallback == "" {
		return nil, fmt.Errorf("fallback suggested but no fallback configured for path %s", failedReq.Path)
	}
	if time.Now().After(deadline) {
		return nil, fmt.Errorf("healing latency budget exhausted before fallback")
	}

	fallbackReq := *failedReq // shallow copy: headers are only read, never mutated
	fallbackReq.Upstream = failedReq.Fallback + failedReq.Path

	slog.Info("healing fallback attempt",
		"request_id", failedReq.RequestID,
		"fallback_url", fallbackReq.Upstream,
	)

	result, ok := e.replayOnce(&fallbackReq, suggestion, 1, ActionFallback, replays)
	if !ok {
		return nil, fmt.Errorf("fallback at %s failed", fallbackReq.Upstream)
	}
	return result, nil
}

// replayOnce performs a single replay and classifies the outcome.
// It returns (result, true) on a healed response, or (nil, false) on any
// failure — transport error, unreadable body, or status >= 400.
func (e *Executor) replayOnce(req *errors.FailedRequest, suggestion *HealingSuggestion, attempt int, action HealingAction, replays *int) (*HealingResult, bool) {
	*replays++
	resp, err := e.replayer.ReplayRequest(req)
	if err != nil {
		slog.Warn("healing replay failed",
			"request_id", req.RequestID,
			"action", action,
			"attempt", attempt,
			"error", err,
		)
		return nil, false
	}

	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		slog.Warn("healing replay: failed to read response body",
			"request_id", req.RequestID,
			"action", action,
			"attempt", attempt,
			"error", readErr,
		)
		return nil, false
	}

	if resp.StatusCode >= 400 {
		slog.Warn("healing replay returned failure status",
			"request_id", req.RequestID,
			"action", action,
			"attempt", attempt,
			"status", resp.StatusCode,
		)
		return nil, false
	}

	slog.Info("healing succeeded",
		"request_id", req.RequestID,
		"action", action,
		"attempt", attempt,
		"status", resp.StatusCode,
	)
	return &HealingResult{
		Action:     action,
		Reasoning:  suggestion.Reasoning,
		Attempts:   attempt,
		StatusCode: resp.StatusCode,
		Header:     resp.Header,
		Body:       body,
	}, true
}
