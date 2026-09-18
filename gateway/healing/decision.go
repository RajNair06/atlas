package healing

import (
	"encoding/json"
	"fmt"
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

// AnalyzeError analyzes a failed request and returns a healing suggestion
func (d *DecisionEngine) AnalyzeError(failedReq *errors.FailedRequest) (*HealingSuggestion, error) {
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
1. retry - Retry the same request (for transient failures like timeouts, 503, 504, connection errors)
2. fallback - Call the configured fallback endpoint (for permanent failures, use the fallback URL shown above)
3. give_up - Give up and return error to client (for client errors like 400, 401, 404, or when retry won't help)

Return ONLY a JSON object with this exact structure:
{
  "action": "retry" | "fallback" | "give_up",
  "reasoning": "brief explanation of why this action",
  "fallback_path": "/alternative/endpoint" (only include if action is fallback)
}

Example responses:
{"action":"retry","reasoning":"503 Service Unavailable is often transient, retry may succeed"}
{"action":"fallback","reasoning":"Payment service is down, use configured backup payment endpoint","fallback_path":"/backup/pay"}
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
