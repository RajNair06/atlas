package proxy

import (
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/RajNair06/atlas/gateway/config"
)

// Gateway is the reverse proxy that routes requests to upstream services
type Gateway struct {
	config *config.Config
	client *http.Client
}

// New creates a new Gateway with the given config
func New(cfg *config.Config) *Gateway {
	return &Gateway{
		config: cfg,
		client: &http.Client{},
	}
}

// ServeHTTP handles incoming HTTP requests
// This is the method that makes Gateway implement http.Handler
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

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
	
	// If strip_prefix is enabled, remove the matched path prefix
	if route.StripPrefix {
		// For now, we only support exact matches, so stripping means forwarding to root
		upstreamURL = route.Upstream
	}

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
		"path", r.URL.Path,
		"upstream", upstreamURL,
		"method", r.Method,
	)

	resp, err := client.Do(upstreamReq)
	if err != nil {
		slog.Error("upstream request failed",
			"error", err,
			"path", r.URL.Path,
			"upstream", route.Upstream,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Copy status code
	w.WriteHeader(resp.StatusCode)

	// Copy response body
	io.Copy(w, resp.Body)

	slog.Info("request completed",
		"path", r.URL.Path,
		"status", resp.StatusCode,
		"duration_ms", time.Since(start).Milliseconds(),
	)
}

// findRoute looks up the route for a given path
// Currently only supports exact matches
func (g *Gateway) findRoute(path string) (*config.RouteConfig, bool) {
	for i := range g.config.Routes {
		if g.config.Routes[i].Path == path {
			return &g.config.Routes[i], true
		}
	}
	return nil, false
}
