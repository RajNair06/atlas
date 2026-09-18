package proxy

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
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

// ServeHTTP handles incoming HTTP requests
// This is the method that makes Gateway implement http.Handler
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Extract request ID from headers (set by correlation ID middleware)
	requestID := r.Header.Get("X-Request-ID")

	// Read request body if present (so we can store it on error)
	var requestBody string
	if r.Body != nil {
		bodyBytes, err := io.ReadAll(r.Body)
		if err == nil {
			requestBody = string(bodyBytes)
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
			ErrorBody:       string(responseBody),
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
