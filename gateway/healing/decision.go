package healing

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/RajNair06/atlas/gateway/errors"
	"github.com/RajNair06/atlas/gateway/llm"
)

// HealingAction represents the type of healing action to take
type HealingAction string

const (
	ActionRetry    HealingAction = "retry"
	ActionFallback HealingAction = "fallback"
	ActionGiveUp   HealingAction = "give_up"
)

// HealingSuggestion represents Gemini's recommendation for handling a failed request
type HealingSuggestion struct {
	Action       HealingAction `json:"action"`
	Reasoning    string        `json:"reasoning"`
	FallbackPath string        `json:"fallback_path,omitempty"`
	Timestamp    time.Time     `json:"timestamp"`
}

// DecisionEngine uses Gemini to analyze errors and suggest healing actions
type DecisionEngine struct {
	llmClient *llm.GeminiClient
}

// NewDecisionEngine creates a new decision engine
func NewDecisionEngine(llmClient *llm.GeminiClient) *DecisionEngine {
	return &DecisionEngine{
		llmClient: llmClient,
	}
}

// AnalyzeError analyzes a failed request and returns a healing suggestion.
// Gemini is the primary brain; when it is unreachable (overloaded, network
// down, bad key) a deterministic rule-based suggestion takes over so the
// healing pipeline — and the approval console — never stall on an external
// dependency. The reasoning string always says which brain produced it.
func (d *DecisionEngine) AnalyzeError(failedReq *errors.FailedRequest) (*HealingSuggestion, error) {
	suggestion, err := d.analyzeWithLLM(failedReq)
	if err == nil {
		return suggestion, nil
	}

	slog.Warn("Gemini analysis failed, falling back to rule-based suggestion",
		"request_id", failedReq.RequestID,
		"error", err,
	)
	return ruleBasedSuggestion(failedReq), nil
}

// analyzeWithLLM is the Gemini path: build the prompt, call the model,
// parse and validate the structured suggestion.
func (d *DecisionEngine) analyzeWithLLM(failedReq *errors.FailedRequest) (*HealingSuggestion, error) {
	prompt := buildPrompt(failedReq)

	responseText, err := d.llmClient.GenerateContent(prompt)
	if err != nil {
		return nil, fmt.Errorf("Gemini analysis failed: %w", err)
	}

	suggestion, err := parseResponse(responseText)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Gemini response: %w", err)
	}

	suggestion.Timestamp = time.Now()
	return suggestion, nil
}

// ruleBasedSuggestion mirrors the guidance we give Gemini, in code:
// transport failures, timeouts and 5xx are usually transient → retry
// (the executor's last-resort chain still reaches the fallback if one is
// configured); other 4xx are client errors → retrying cannot help → give_up.
func ruleBasedSuggestion(failedReq *errors.FailedRequest) *HealingSuggestion {
	body := strings.ToLower(failedReq.ErrorBody)
	transient := failedReq.StatusCode >= 500 ||
		failedReq.StatusCode == http.StatusRequestTimeout ||
		failedReq.StatusCode == http.StatusTooManyRequests ||
		strings.Contains(body, "connection refused") ||
		strings.Contains(body, "unreachable") ||
		strings.Contains(body, "timeout") ||
		strings.Contains(body, "eof")

	if transient {
		return &HealingSuggestion{
			Action: ActionRetry,
			Reasoning: fmt.Sprintf("rule-based fallback (Gemini unavailable): status %d with %q looks transient — restarts and brief overloads usually clear, so retry",
				failedReq.StatusCode, excerpt(failedReq.ErrorBody, 80)),
			Timestamp: time.Now(),
		}
	}
	return &HealingSuggestion{
		Action: ActionGiveUp,
		Reasoning: fmt.Sprintf("rule-based fallback (Gemini unavailable): status %d is a client-side error — retrying the same request cannot change the outcome",
			failedReq.StatusCode),
		Timestamp: time.Now(),
	}
}

// buildPrompt creates the prompt for Gemini based on the failed request
func buildPrompt(failedReq *errors.FailedRequest) string {
	return fmt.Sprintf(`You are an API gateway error analyzer. Analyze this failed request and suggest a healing action.

Failed Request Context:
- Request ID: %s
- Method: %s
- Path: %s
- Upstream: %s
- Configured Fallback: %s
- Status Code: %d
- Error Body: %s
- Duration: %dms
- Request Headers: %v
- Response Headers: %v

Available Actions:
1. retry - Retry the same request. Use for transient failures: timeouts, 502, 503, 504, "connection refused", "unreachable". In microservices these usually mean a service is restarting, deploying, or briefly overloaded — a retry often succeeds even when no fallback exists.
2. fallback - Call the configured fallback endpoint. ONLY choose this when the failure looks permanent AND the "Configured Fallback" field above is non-empty.
3. give_up - Give up and return the error to the client. Use for client errors (400, 401, 403, 404) where retrying cannot possibly help, or for explicit business-logic rejections.

Return ONLY a JSON object with this exact structure:
{
  "action": "retry" | "fallback" | "give_up",
  "reasoning": "brief explanation of why this action",
  "fallback_path": "/alternative/endpoint" (only include if action is fallback)
}

Example responses:
{"action":"retry","reasoning":"connection refused is usually transient — the upstream may be restarting, retry may succeed"}
{"action":"retry","reasoning":"503 Service Unavailable is often transient, retry may succeed"}
{"action":"fallback","reasoning":"Payment service is down and a fallback endpoint is configured","fallback_path":"/backup/pay"}
{"action":"give_up","reasoning":"404 Not Found indicates the resource doesn't exist, retry won't help"}

Your response:`,
		failedReq.RequestID,
		failedReq.Method,
		failedReq.Path,
		failedReq.Upstream,
		failedReq.Fallback,
		failedReq.StatusCode,
		failedReq.ErrorBody,
		failedReq.DurationMs,
		failedReq.RequestHeaders,
		failedReq.ResponseHeaders,
	)
}

// parseResponse parses Gemini's response text into a HealingSuggestion
func parseResponse(responseText string) (*HealingSuggestion, error) {
	var suggestion HealingSuggestion
	if err := json.Unmarshal([]byte(responseText), &suggestion); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w (text: %s)", err, responseText)
	}

	// Validate action
	switch suggestion.Action {
	case ActionRetry, ActionFallback, ActionGiveUp:
		// Valid action
	default:
		return nil, fmt.Errorf("invalid action: %s", suggestion.Action)
	}

	// Validate fallback has a path
	if suggestion.Action == ActionFallback && suggestion.FallbackPath == "" {
		return nil, fmt.Errorf("fallback action requires fallback_path")
	}

	// Validate reasoning is present
	if suggestion.Reasoning == "" {
		return nil, fmt.Errorf("reasoning is required")
	}

	return &suggestion, nil
}
