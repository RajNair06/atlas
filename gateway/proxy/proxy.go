package proxy

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/RajNair06/atlas/gateway/config"
	"github.com/RajNair06/atlas/gateway/errors"
	"github.com/RajNair06/atlas/gateway/healing"
)

// Gateway is the reverse proxy that routes requests to upstream services
type Gateway struct {
	config         *config.Config
	client         *http.Client
	errorStore     *errors.Store
	decisionEngine *healing.DecisionEngine
	executor       *healing.Executor
}

// New creates a new Gateway with the given config
func New(cfg *config.Config, decisionEngine *healing.DecisionEngine) *Gateway {
	return &Gateway{
		config:         cfg,
		client:         &http.Client{},
		errorStore:     errors.NewStore(),
		decisionEngine: decisionEngine,
	}
}

// GetErrorStore returns the error store for external access
func (g *Gateway) GetErrorStore() *errors.Store {
	return g.errorStore
}

// SetExecutor wires the healing executor after construction.
// Two-phase wiring is required because the executor replays requests
// through this gateway: gateway needs executor, executor needs gateway.
func (g *Gateway) SetExecutor(executor *healing.Executor) {
	g.executor = executor
}

// ServeHTTP handles incoming HTTP requests
// This is the method that makes Gateway implement http.Handler
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Extract request ID from headers (set by correlation ID middleware)
	requestID := r.Header.Get("X-Request-ID")

	// Read request body if present (so we can store it on error).
	// The full body is always restored for forwarding — only the captured
	// copy kept for healing is size-capped (errors.MaxCapturedBody).
	var requestBody string
	if r.Body != nil {
		bodyBytes, err := io.ReadAll(r.Body)
		if err == nil {
			requestBody = errors.Truncate(string(bodyBytes))
			// Restore the body for the upstream request
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		}
	}

	// Look up route for this path
	route, ok := g.findRoute(r.URL.Path)
	if !ok {
		slog.Warn("no route found",
			"path", r.URL.Path,
			"method", r.Method,
		)
		http.NotFound(w, r)
		return
	}

	// Forward the request to upstream
	upstreamURL := route.Upstream + r.URL.Path

	// Create the upstream request
	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, r.Body)
	if err != nil {
		slog.Error("failed to create upstream request",
			"error", err,
			"path", r.URL.Path,
			"upstream", route.Upstream,
		)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Copy headers from original request
	for key, values := range r.Header {
		for _, value := range values {
			upstreamReq.Header.Add(key, value)
		}
	}

	// Create a client with the route's timeout
	client := &http.Client{
		Timeout: route.Timeout,
	}

	// Send request to upstream
	slog.Info("forwarding request",
		"request_id", requestID,
		"path", r.URL.Path,
		"upstream", upstreamURL,
		"method", r.Method,
	)

	resp, err := client.Do(upstreamReq)
	if err != nil {
		slog.Error("upstream request failed",
			"request_id", requestID,
			"error", err,
			"path", r.URL.Path,
			"upstream", route.Upstream,
			"duration_ms", time.Since(start).Milliseconds(),
		)

		// Transport-level failure (connection refused, timeout, DNS...).
		// Capture it like any other failure so healing can act on it —
		// "upstream is dead" is the most common failure there is.
		failedReq := &errors.FailedRequest{
			RequestID:       requestID,
			Method:          r.Method,
			Path:            r.URL.Path,
			Upstream:        upstreamURL,
			Fallback:        route.Fallback,
			StatusCode:      http.StatusBadGateway,
			ErrorBody:       errors.Truncate(err.Error()),
			RequestHeaders:  r.Header,
			RequestBody:     requestBody,
			ResponseHeaders: http.Header{},
			Timestamp:       time.Now(),
			DurationMs:      time.Since(start).Milliseconds(),
		}
		if !route.SkipHealing {
			g.errorStore.Add(failedReq)
		}
		if g.attemptHealing(w, failedReq, route, requestID, start) {
			return
		}

		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Read response body into buffer (so we can store it on error)
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Error("failed to read response body",
			"request_id", requestID,
			"error", err,
		)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Check if this is a failure (4xx or 5xx)
	if errors.IsFailure(resp.StatusCode) && !route.SkipHealing {
		// Capture the full context
		failedReq := &errors.FailedRequest{
			RequestID:       requestID,
			Method:          r.Method,
			Path:            r.URL.Path,
			Upstream:        upstreamURL,
			Fallback:        route.Fallback,
			StatusCode:      resp.StatusCode,
			ErrorBody:       errors.Truncate(string(responseBody)),
			RequestHeaders:  r.Header,
			RequestBody:     requestBody,
			ResponseHeaders: resp.Header,
			Timestamp:       time.Now(),
			DurationMs:      time.Since(start).Milliseconds(),
		}

		// Store the failed request
		g.errorStore.Add(failedReq)

		// Log the failure with full context
		slog.Error("request failed - captured for healing",
			"request_id", requestID,
			"path", r.URL.Path,
			"upstream", upstreamURL,
			"status", resp.StatusCode,
			"error_body", string(responseBody),
			"duration_ms", time.Since(start).Milliseconds(),
		)

		// Synchronous healing: the client waits and receives either the
		// healed response or the original error — nothing is written until
		// healing has decided.
		if g.attemptHealing(w, failedReq, route, requestID, start) {
			return
		}
	}

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Copy status code
	w.WriteHeader(resp.StatusCode)

	// Write response body to client
	w.Write(responseBody)

	slog.Info("request completed",
		"request_id", requestID,
		"path", r.URL.Path,
		"status", resp.StatusCode,
		"duration_ms", time.Since(start).Milliseconds(),
	)
}

// attemptHealing runs the healing executor when enabled and, on success,
// writes the healed response (plus X-Healed headers) to the client.
// It reports whether a response was written; when false, the caller must
// write the original error itself.
func (g *Gateway) attemptHealing(w http.ResponseWriter, failedReq *errors.FailedRequest, route *config.RouteConfig, requestID string, start time.Time) bool {
	if g.executor == nil || !g.config.Server.AutoHeal || route.SkipHealing {
		return false
	}

	result, healErr := g.executor.ExecuteHealing(failedReq)
	if healErr != nil {
		slog.Warn("healing failed, returning original error",
			"request_id", requestID,
			"error", healErr,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return false
	}

	slog.Info("request healed",
		"request_id", requestID,
		"action", result.Action,
		"attempts", result.Attempts,
		"status", result.StatusCode,
		"duration_ms", time.Since(start).Milliseconds(),
	)

	for key, values := range result.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.Header().Set("X-Healed", "true")
	w.Header().Set("X-Healing-Action", string(result.Action))
	w.Header().Set("X-Healing-Attempts", strconv.Itoa(result.Attempts))
	w.WriteHeader(result.StatusCode)
	_, _ = w.Write(result.Body)
	return true
}

// findRoute looks up the route for a given path
// Uses exact matching only
func (g *Gateway) findRoute(path string) (*config.RouteConfig, bool) {
	for i := range g.config.Routes {
		if g.config.Routes[i].Path == path {
			return &g.config.Routes[i], true
		}
	}
	return nil, false
}

// ReplayRequest re-sends a captured failed request directly to its upstream.
// This makes *Gateway satisfy healing.RequestReplayer. The caller owns the
// response and must read and close resp.Body. Note: it replays the captured
// (size-capped) request body, not the original stream.
func (g *Gateway) ReplayRequest(failedReq *errors.FailedRequest) (*http.Response, error) {
	var body io.Reader
	if failedReq.RequestBody != "" {
		body = strings.NewReader(failedReq.RequestBody)
	}

	req, err := http.NewRequest(failedReq.Method, failedReq.Upstream, body)
	if err != nil {
		return nil, err
	}

	for key, values := range failedReq.RequestHeaders {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	timeout := 5 * time.Second
	if route, ok := g.findRoute(failedReq.Path); ok {
		timeout = route.Timeout
	}

	client := &http.Client{Timeout: timeout}

	slog.Info("replaying request",
		"request_id", failedReq.RequestID,
		"method", failedReq.Method,
		"upstream", failedReq.Upstream,
	)

	return client.Do(req)
}
