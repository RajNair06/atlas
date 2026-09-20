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
}

// Executor performs the healing actions suggested by the decision engine,
// guarded by a per-upstream circuit breaker and a per-request latency budget.
type Executor struct {
	analyzer      Analyzer
	replayer      RequestReplayer
	maxAttempts   int
	latencyBudget time.Duration
	breakers      *BreakerRegistry
}

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
	return &Executor{
		analyzer:      analyzer,
		replayer:      replayer,
		maxAttempts:   opts.MaxAttempts,
		latencyBudget: opts.LatencyBudget,
		breakers:      NewBreakerRegistry(opts.BreakerThreshold, opts.BreakerReset),
	}
}

const retryBaseDelay = 100 * time.Millisecond

// ExecuteHealing is the full healing pipeline for one captured failure:
// breaker gate → analyze with the LLM → execute the suggested action →
// (retry only) fall through to the configured fallback as a last resort.
func (e *Executor) ExecuteHealing(failedReq *errors.FailedRequest) (*HealingResult, error) {
	breaker := e.breakers.For(failedReq.Upstream)
	if !breaker.Allow() {
		return nil, fmt.Errorf("circuit open for %s: failing fast, skipping LLM and retries", failedReq.Upstream)
	}
	// We are here because the upstream request failed — count it immediately.
	// A successful heal below will reset the count.
	breaker.RecordFailure()

	deadline := time.Now().Add(e.latencyBudget)

	suggestion, err := e.analyzer.AnalyzeError(failedReq)
	if err != nil {
		return nil, fmt.Errorf("analysis failed: %w", err)
	}

	slog.Info("healing suggestion received",
		"request_id", failedReq.RequestID,
		"action", suggestion.Action,
		"reasoning", suggestion.Reasoning,
	)

	var result *HealingResult

	switch suggestion.Action {
	case ActionRetry:
		result, err = e.executeRetry(failedReq, suggestion, deadline)
		// Last resort: retries exhausted, a fallback is configured, budget remains.
		if err != nil && failedReq.Fallback != "" && time.Now().Before(deadline) {
			slog.Info("retries exhausted, trying configured fallback as last resort",
				"request_id", failedReq.RequestID,
				"fallback", failedReq.Fallback,
			)
			result, err = e.executeFallback(failedReq, suggestion, deadline)
		}
	case ActionFallback:
		result, err = e.executeFallback(failedReq, suggestion, deadline)
	case ActionGiveUp:
		err = fmt.Errorf("gemini recommended give_up: %s", suggestion.Reasoning)
	default:
		err = fmt.Errorf("unknown action: %s", suggestion.Action)
	}

	if err != nil {
		return nil, err
	}

	breaker.RecordSuccess()
	return result, nil
}

// executeRetry replays the request with exponential backoff: 100ms, 200ms, 400ms...
// It stops at the first success (status < 400), the latency budget, or maxAttempts.
func (e *Executor) executeRetry(failedReq *errors.FailedRequest, suggestion *HealingSuggestion, deadline time.Time) (*HealingResult, error) {
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

		result, ok := e.replayOnce(failedReq, suggestion, attempt, ActionRetry)
		if ok {
			return result, nil
		}
	}

	return nil, fmt.Errorf("all %d retry attempts failed", e.maxAttempts)
}

// executeFallback replays the request against the route's configured fallback
// URL (fallback base + original path). A single attempt; the caller decides
// whether it is a direct suggestion or a last resort after retries.
func (e *Executor) executeFallback(failedReq *errors.FailedRequest, suggestion *HealingSuggestion, deadline time.Time) (*HealingResult, error) {
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

	result, ok := e.replayOnce(&fallbackReq, suggestion, 1, ActionFallback)
	if !ok {
		return nil, fmt.Errorf("fallback at %s failed", fallbackReq.Upstream)
	}
	return result, nil
}

// replayOnce performs a single replay and classifies the outcome.
// It returns (result, true) on a healed response, or (nil, false) on any
// failure — transport error, unreadable body, or status >= 400.
func (e *Executor) replayOnce(req *errors.FailedRequest, suggestion *HealingSuggestion, attempt int, action HealingAction) (*HealingResult, bool) {
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
