package healing

import (
	"strings"
	"testing"

	"github.com/RajNair06/atlas/gateway/errors"
)

func TestBuildPromptIncludesFallback(t *testing.T) {
	req := &errors.FailedRequest{
		RequestID:  "test-123",
		Method:     "POST",
		Path:       "/pay",
		Upstream:   "http://localhost:8083",
		Fallback:   "http://localhost:8084",
		StatusCode: 502,
		ErrorBody:  "payment unreachable",
		DurationMs: 12,
	}

	prompt := buildPrompt(req)

	if !strings.Contains(prompt, "Configured Fallback: http://localhost:8084") {
		t.Fatalf("prompt does not include the configured fallback URL:\n%s", prompt)
	}
	if !strings.Contains(prompt, "test-123") {
		t.Fatal("prompt does not include the request ID")
	}
	if !strings.Contains(prompt, "/pay") {
		t.Fatal("prompt does not include the request path")
	}
}

func TestParseResponseRetry(t *testing.T) {
	s, err := parseResponse(`{"action":"retry","reasoning":"transient 503"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Action != ActionRetry {
		t.Fatalf("action = %s, want retry", s.Action)
	}
}

func TestParseResponseFallback(t *testing.T) {
	s, err := parseResponse(`{"action":"fallback","reasoning":"upstream down","fallback_path":"http://localhost:8084"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Action != ActionFallback {
		t.Fatalf("action = %s, want fallback", s.Action)
	}
	if s.FallbackPath != "http://localhost:8084" {
		t.Fatalf("fallback_path = %s, want http://localhost:8084", s.FallbackPath)
	}
}

func TestParseResponseGiveUp(t *testing.T) {
	s, err := parseResponse(`{"action":"give_up","reasoning":"404 means the resource does not exist"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Action != ActionGiveUp {
		t.Fatalf("action = %s, want give_up", s.Action)
	}
}

func TestParseResponseInvalidAction(t *testing.T) {
	if _, err := parseResponse(`{"action":"reboot_server","reasoning":"why not"}`); err == nil {
		t.Fatal("expected error for invalid action, got nil")
	}
}

func TestParseResponseFallbackWithoutPath(t *testing.T) {
	if _, err := parseResponse(`{"action":"fallback","reasoning":"upstream down"}`); err == nil {
		t.Fatal("expected error when fallback_path is missing, got nil")
	}
}

func TestParseResponseMissingReasoning(t *testing.T) {
	if _, err := parseResponse(`{"action":"retry"}`); err == nil {
		t.Fatal("expected error when reasoning is missing, got nil")
	}
}

func TestParseResponseMalformedJSON(t *testing.T) {
	if _, err := parseResponse(`Sure! I think you should retry.`); err == nil {
		t.Fatal("expected error for non-JSON response, got nil")
	}
}
