package healing

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RajNair06/atlas/gateway/errors"
	"github.com/RajNair06/atlas/gateway/llm"
)

// downLLMServer mimics the Gemini free tier during a demand spike: 503s.
func downLLMServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"code":503,"message":"high demand"}}`, http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func engineAgainst(t *testing.T, srv *httptest.Server) *DecisionEngine {
	t.Helper()
	client := llm.NewGeminiClientWithBaseURL("test-key", "test-model", 2*time.Second, srv.URL+"/")
	return NewDecisionEngine(client)
}

func TestAnalyzeErrorFallsBackToRulesWhenLLMDown(t *testing.T) {
	engine := engineAgainst(t, downLLMServer(t))

	// 5xx → transient → retry
	s, err := engine.AnalyzeError(&errors.FailedRequest{
		RequestID: "r1", StatusCode: http.StatusBadGateway, ErrorBody: "order failed",
	})
	if err != nil {
		t.Fatalf("LLM outage must not fail the analysis, got: %v", err)
	}
	if s.Action != ActionRetry {
		t.Fatalf("action = %s, want retry for a 502", s.Action)
	}
	if !strings.Contains(s.Reasoning, "rule-based fallback") {
		t.Fatalf("reasoning must disclose the degraded mode: %q", s.Reasoning)
	}
	if s.Timestamp.IsZero() {
		t.Fatal("timestamp must be set")
	}
}

func TestRuleFallbackClassifiesClientErrorsAsGiveUp(t *testing.T) {
	engine := engineAgainst(t, downLLMServer(t))

	s, err := engine.AnalyzeError(&errors.FailedRequest{
		RequestID: "r2", StatusCode: http.StatusNotFound, ErrorBody: "not found",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Action != ActionGiveUp {
		t.Fatalf("action = %s, want give_up for a 404", s.Action)
	}
}

func TestRuleFallbackDetectsTransportSignatures(t *testing.T) {
	engine := engineAgainst(t, downLLMServer(t))

	// A 502 whose body says connection refused must read as transient.
	s, err := engine.AnalyzeError(&errors.FailedRequest{
		RequestID: "r3", StatusCode: http.StatusBadGateway,
		ErrorBody: `payment unreachable: Post "http://localhost:8083/pay": dial tcp 127.0.0.1:8083: connect: connection refused`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Action != ActionRetry {
		t.Fatalf("action = %s, want retry for connection-refused", s.Action)
	}
}
