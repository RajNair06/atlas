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

// Executor performs the healing actions suggested by the decision engine.
type Executor struct {
	decisionEngine *DecisionEngine
	replayer       RequestReplayer
	maxAttempts    int
}

// NewExecutor creates a healing executor.
func NewExecutor(decisionEngine *DecisionEngine, replayer RequestReplayer, maxAttempts int) *Executor {
	return &Executor{
		decisionEngine: decisionEngine,
		replayer:       replayer,
		maxAttempts:    maxAttempts,
	}
}

const retryBaseDelay = 100 * time.Millisecond

// ExecuteHealing asks the decision engine what to do about a failed request,
// then carries out the suggested action. On success it returns the healed
// response; on failure it returns an error describing why healing gave up.
func (e *Executor) ExecuteHealing(failedReq *errors.FailedRequest) (*HealingResult, error) {
	suggestion, err := e.decisionEngine.AnalyzeError(failedReq)
	if err != nil {
		return nil, fmt.Errorf("analysis failed: %w", err)
	}

	slog.Info("healing suggestion received",
		"request_id", failedReq.RequestID,
		"action", suggestion.Action,
		"reasoning", suggestion.Reasoning,
	)

	switch suggestion.Action {
	case ActionRetry:
		return e.executeRetry(failedReq, suggestion)
	case ActionFallback:
		return nil, fmt.Errorf("fallback action not implemented yet (suggested path: %s)", suggestion.FallbackPath)
	case ActionGiveUp:
		return nil, fmt.Errorf("gemini recommended give_up: %s", suggestion.Reasoning)
	default:
		return nil, fmt.Errorf("unknown action: %s", suggestion.Action)
	}
}

// executeRetry replays the request with exponential backoff: 100ms, 200ms, 400ms...
// It stops at the first success (status < 400) or after maxAttempts failures.
func (e *Executor) executeRetry(failedReq *errors.FailedRequest, suggestion *HealingSuggestion) (*HealingResult, error) {
	delay := retryBaseDelay

	for attempt := 1; attempt <= e.maxAttempts; attempt++ {
		slog.Info("healing retry attempt",
			"request_id", failedReq.RequestID,
			"attempt", attempt,
			"max_attempts", e.maxAttempts,
			"delay_ms", delay.Milliseconds(),
		)
		time.Sleep(delay)
		delay *= 2

		resp, err := e.replayer.ReplayRequest(failedReq)
		if err != nil {
			slog.Warn("retry attempt failed",
				"request_id", failedReq.RequestID,
				"attempt", attempt,
				"error", err,
			)
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			slog.Warn("retry attempt: failed to read response body",
				"request_id", failedReq.RequestID,
				"attempt", attempt,
				"error", readErr,
			)
			continue
		}

		if resp.StatusCode < 400 {
			slog.Info("healing retry succeeded",
				"request_id", failedReq.RequestID,
				"attempt", attempt,
				"status", resp.StatusCode,
			)
			return &HealingResult{
				Action:     ActionRetry,
				Reasoning:  suggestion.Reasoning,
				Attempts:   attempt,
				StatusCode: resp.StatusCode,
				Header:     resp.Header,
				Body:       body,
			}, nil
		}

		slog.Warn("retry attempt returned failure status",
			"request_id", failedReq.RequestID,
			"attempt", attempt,
			"status", resp.StatusCode,
		)
	}

	return nil, fmt.Errorf("all %d retry attempts failed", e.maxAttempts)
}
