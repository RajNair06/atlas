package healing

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/RajNair06/atlas/gateway/errors"
)

// Decision is a human verdict on a pending healing decision.
// Reason carries the operator's rejection note (empty on approval);
// DecidedAt is when the verdict was submitted.
type Decision struct {
	Approved  bool      `json:"approved"`
	Reason    string    `json:"reason,omitempty"`
	DecidedAt time.Time `json:"decided_at"`
}

// Outcome is the final fate of a gated healing decision, as shown in the
// console's history feed:
//   - healed:   approved, execution produced a successful response
//   - failed:   approved, but retries/fallback could not heal the request
//   - rejected: an operator refused the suggestion
//   - expired:  no verdict arrived before the approval timeout
type Outcome string

const (
	OutcomeHealed   Outcome = "healed"
	OutcomeFailed   Outcome = "failed"
	OutcomeRejected Outcome = "rejected"
	OutcomeExpired  Outcome = "expired"
)

// PendingDecision is the full context a human needs to judge one healing
// suggestion: which request failed, how, and what Gemini proposes to do.
// It is deliberately flat and JSON-friendly — the console renders it as-is.
type PendingDecision struct {
	ID           string        `json:"id"`
	RequestID    string        `json:"request_id"`
	Method       string        `json:"method"`
	Path         string        `json:"path"`
	Upstream     string        `json:"upstream"`
	StatusCode   int           `json:"status_code"`
	ErrorExcerpt string        `json:"error_excerpt,omitempty"`
	Action       HealingAction `json:"action"`
	Reasoning    string        `json:"reasoning"`
	FallbackPath string        `json:"fallback_path,omitempty"`
	Fallback     string        `json:"fallback,omitempty"` // route's configured fallback base URL
	CreatedAt    time.Time     `json:"created_at"`
}

// Approver is the human-in-the-loop gate. When an Executor holds a non-nil
// Approver, every healing suggestion is published as a PendingDecision and
// execution blocks until a person approves, rejects, or the timeout elapses.
// Implemented by approval.Store; faked in tests. The interface lives here
// (where it is used) so healing never imports the store or the web UI.
type Approver interface {
	// Enabled reports whether the gate is currently armed. The console can
	// toggle it at runtime; when false, healing proceeds without asking and
	// no pending decisions or history entries are produced.
	Enabled() bool

	// AwaitApproval registers the pending decision, notifies subscribers
	// (the UI), and blocks until a verdict arrives or timeout elapses.
	// Approved decisions return (decision, nil); rejections return the
	// decision plus a "rejected by operator" error; timeouts return an
	// "approval timed out" error and mark the decision expired.
	AwaitApproval(pending PendingDecision, timeout time.Duration) (Decision, error)

	// RecordOutcome finalizes the history entry for an approved decision
	// once execution finishes: healed or failed, with replay count and
	// execution duration. Only called after AwaitApproval returned nil.
	RecordOutcome(id string, outcome Outcome, attempts int, execution time.Duration)
}

// errorExcerptLimit caps how much of the error body is carried into a
// PendingDecision — enough for a human to recognize the failure, short
// enough to render in one console card.
const errorExcerptLimit = 240

// NewPendingDecision builds the approval payload for one failed request and
// its Gemini suggestion, with a fresh unique decision ID.
func NewPendingDecision(failedReq *errors.FailedRequest, suggestion *HealingSuggestion) PendingDecision {
	return PendingDecision{
		ID:           newDecisionID(),
		RequestID:    failedReq.RequestID,
		Method:       failedReq.Method,
		Path:         failedReq.Path,
		Upstream:     failedReq.Upstream,
		StatusCode:   failedReq.StatusCode,
		ErrorExcerpt: excerpt(failedReq.ErrorBody, errorExcerptLimit),
		Action:       suggestion.Action,
		Reasoning:    suggestion.Reasoning,
		FallbackPath: suggestion.FallbackPath,
		Fallback:     failedReq.Fallback,
		CreatedAt:    time.Now(),
	}
}

// newDecisionID returns 8 random bytes as hex — unique for any realistic
// console workload, and short enough to display and to put in a URL.
func newDecisionID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is effectively unheard of; fall back to a
		// timestamp-derived ID rather than blocking the healing path.
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// excerpt trims whitespace and truncates s to at most limit runes,
// appending an ellipsis when anything was cut.
func excerpt(s string, limit int) string {
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}
	// Cut on rune boundaries so multi-byte characters survive truncation.
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "..."
}
