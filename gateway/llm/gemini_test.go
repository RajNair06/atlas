package llm

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testKey is a fake API key used across tests. Redaction tests assert this
// exact string never appears in error output.
const testKey = "fake-key-ABC123"

// geminiResponse builds a well-formed Gemini response body carrying text.
func geminiResponse(text string) string {
	resp := GeminiResponse{
		Candidates: []Candidate{
			{Content: Content{Parts: []Part{{Text: text}}}},
		},
	}
	b, err := json.Marshal(resp)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// fakeGemini spins up an httptest server serving handler at the exact path
// the client builds, and returns a GeminiClient pointed at it (baseURL with
// trailing "/" so the client's URL concatenation stays byte-identical to
// production).
func fakeGemini(t *testing.T, handler http.HandlerFunc) *GeminiClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewGeminiClientWithBaseURL(testKey, "gemini-test", 2*time.Second, srv.URL+"/")
}

func TestGenerateContentHappyPath(t *testing.T) {
	const prompt = "analyze this 503 and suggest a healing action"

	var gotPath, gotKey, gotContentType string
	var gotBody GeminiRequest

	client := fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.URL.Query().Get("key")
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Errorf("request body is not valid GeminiRequest JSON: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, geminiResponse(`{"action":"retry","reasoning":"transient"}`))
	})

	text, err := client.GenerateContent(prompt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Response extraction: candidates[0].content.parts[0].text
	if text != `{"action":"retry","reasoning":"transient"}` {
		t.Fatalf("text = %q, want the candidate's part text", text)
	}

	// Request shape: URL path, key parameter, content type
	if want := "/v1beta/models/gemini-test:generateContent"; gotPath != want {
		t.Fatalf("path = %q, want %q", gotPath, want)
	}
	if gotKey != testKey {
		t.Fatalf("key query param = %q, want %q", gotKey, testKey)
	}
	if gotContentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", gotContentType)
	}

	// Request shape: prompt rides in contents[0].parts[0].text
	if len(gotBody.Contents) != 1 || len(gotBody.Contents[0].Parts) != 1 {
		t.Fatalf("contents shape = %+v, want exactly one content with one part", gotBody.Contents)
	}
	if gotBody.Contents[0].Parts[0].Text != prompt {
		t.Fatalf("prompt = %q, want %q", gotBody.Contents[0].Parts[0].Text, prompt)
	}

	// Request shape: generationConfig
	if gotBody.GenerationConfig.Temperature != 0.3 {
		t.Fatalf("temperature = %v, want 0.3", gotBody.GenerationConfig.Temperature)
	}
	if gotBody.GenerationConfig.MaxOutputTokens != 256 {
		t.Fatalf("maxOutputTokens = %d, want 256", gotBody.GenerationConfig.MaxOutputTokens)
	}
}

func TestGenerateContentStripsMarkdownFences(t *testing.T) {
	wrapped := "```json\n{\"action\":\"give_up\",\"reasoning\":\"permanent 404\"}\n```"

	client := fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, geminiResponse(wrapped))
	})

	text, err := client.GenerateContent("prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != `{"action":"give_up","reasoning":"permanent 404"}` {
		t.Fatalf("text = %q, want the fence-stripped JSON", text)
	}
	if strings.Contains(text, "```") {
		t.Fatalf("markdown fences survived stripping: %q", text)
	}
}

func TestGenerateContentHTTPErrorIncludesStatus(t *testing.T) {
	client := fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"quota exceeded"}}`)
	})

	_, err := client.GenerateContent("prompt")
	if err == nil {
		t.Fatal("expected error for HTTP 429, got nil")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Fatalf("error = %q, want it to include the status code", err)
	}
	if !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("error = %q, want it to include the API error body", err)
	}
}

func TestGenerateContentHTMLResponse(t *testing.T) {
	client := fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body><h1>502 Bad Gateway</h1></body></html>`)
	})

	_, err := client.GenerateContent("prompt")
	if err == nil {
		t.Fatal("expected error for HTML response, got nil")
	}
	if !strings.Contains(err.Error(), "HTML") {
		t.Fatalf("error = %q, want a clear HTML-instead-of-JSON message", err)
	}
}

func TestGenerateContentEmptyCandidates(t *testing.T) {
	client := fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"candidates":[]}`)
	})

	_, err := client.GenerateContent("prompt")
	if err == nil {
		t.Fatal("expected error for empty candidates, got nil")
	}
	if !strings.Contains(err.Error(), "empty candidates") {
		t.Fatalf("error = %q, want it to mention empty candidates", err)
	}
}

func TestGenerateContentEmptyParts(t *testing.T) {
	client := fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"candidates":[{"content":{"parts":[]}}]}`)
	})

	_, err := client.GenerateContent("prompt")
	if err == nil {
		t.Fatal("expected error for empty parts, got nil")
	}
	if !strings.Contains(err.Error(), "empty parts") {
		t.Fatalf("error = %q, want it to mention empty parts", err)
	}
}

func TestGenerateContentMalformedJSON(t *testing.T) {
	client := fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"candidates": [oops`)
	})

	_, err := client.GenerateContent("prompt")
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Fatalf("error = %q, want it to mention parsing", err)
	}
}

func TestGenerateContentTimeout(t *testing.T) {
	// The server stalls longer than the client timeout; GenerateContent must
	// surface a timeout error quickly (the whole test stays under ~500ms —
	// server cleanup waits out the remaining stall).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		fmt.Fprint(w, geminiResponse("too late"))
	}))
	t.Cleanup(srv.Close)

	client := NewGeminiClientWithBaseURL(testKey, "gemini-test", 10*time.Millisecond, srv.URL+"/")

	start := time.Now()
	_, err := client.GenerateContent("prompt")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("timeout took %s, want the client timeout to cut it short", elapsed)
	}
	if strings.Contains(err.Error(), testKey) {
		t.Fatalf("timeout error leaks the API key: %q", err)
	}
}

func TestGenerateContentRedactsKeyOnTransportError(t *testing.T) {
	// A server that is created and immediately closed gives us a URL whose
	// port refuses connections — the transport error embeds the full URL
	// (including ?key=...), which must be redacted.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := srv.URL
	srv.Close()

	client := NewGeminiClientWithBaseURL(testKey, "gemini-test", time.Second, deadURL+"/")

	_, err := client.GenerateContent("prompt")
	if err == nil {
		t.Fatal("expected transport error, got nil")
	}
	if strings.Contains(err.Error(), testKey) {
		t.Fatalf("transport error leaks the API key: %q", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("error = %q, want the key replaced with [REDACTED]", err)
	}
}

func TestGenerateContentRedactsKeyInErrorBody(t *testing.T) {
	client := fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		// Some APIs echo the offending key back in the error body.
		fmt.Fprintf(w, `{"error":"invalid key: %s"}`, testKey)
	})

	_, err := client.GenerateContent("prompt")
	if err == nil {
		t.Fatal("expected error for HTTP 403, got nil")
	}
	if strings.Contains(err.Error(), testKey) {
		t.Fatalf("error body leaks the API key: %q", err)
	}
}

func TestGenerateContentEmptyKeyNeverCallsAPI(t *testing.T) {
	// The handler fails the test if it is ever reached: with an empty key,
	// GenerateContent must return immediately without an HTTP call.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("server was called despite empty API key")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	client := NewGeminiClientWithBaseURL("", "gemini-test", time.Second, srv.URL+"/")

	_, err := client.GenerateContent("prompt")
	if err == nil {
		t.Fatal("expected error for empty API key, got nil")
	}
	if !strings.Contains(err.Error(), "GEMINI_API_KEY not configured") {
		t.Fatalf("error = %q, want the missing-key message", err)
	}
}

func TestRedactKey(t *testing.T) {
	if got := redactKey("url?key=secret123 failed", "secret123"); got != "url?key=[REDACTED] failed" {
		t.Fatalf("redactKey = %q", got)
	}
	// An empty key must leave the message untouched (ReplaceAll with ""
	// would mangle every character boundary).
	if got := redactKey("plain message", ""); got != "plain message" {
		t.Fatalf("redactKey with empty key = %q", got)
	}
}
