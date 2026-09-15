package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/RajNair06/atlas/gateway/config"
	"github.com/RajNair06/atlas/gateway/proxy"
)

func main() {
	// Parse command-line flags
	configPath := flag.String("config", "gateway.yaml", "path to config file")
	flag.Parse()

	// Set up structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "gateway")
	slog.SetDefault(logger)

	slog.Info("starting gateway",
		"config", *configPath,
	)

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	slog.Info("config loaded",
		"port", cfg.Server.Port,
		"routes", len(cfg.Routes),
		"llm_provider", cfg.LLM.Provider,
		"llm_model", cfg.LLM.Model,
	)

	// Create gateway
	gw := proxy.New(cfg)

	// Wrap gateway with correlation ID middleware
	handler := withCorrelationID(gw)

	// Create HTTP server
	server := &http.Server{
		Addr:    ":" + strconv.Itoa(cfg.Server.Port),
		Handler: handler,
	}

	// Set up graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Start server in a goroutine
	go func() {
		slog.Info("gateway listening", "addr", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Wait for interrupt signal
	<-ctx.Done()
	slog.Info("shutting down")

	// Give outstanding requests 5 seconds to complete
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("shutdown error: %v", err)
	}

	slog.Info("gateway stopped")
}

// withCorrelationID is middleware that adds a unique request ID to every request
// The ID is generated at the gateway (edge) and passed to all downstream services via X-Request-ID header
func withCorrelationID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Generate or extract correlation ID
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = generateRequestID()
		}

		// Add to request headers (will be forwarded to upstream)
		r.Header.Set("X-Request-ID", requestID)

		// Add to response headers (client can see it)
		w.Header().Set("X-Request-ID", requestID)

		// Log with request ID
		slog.Info("request received",
			"request_id", requestID,
			"method", r.Method,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
		)

		// Call next handler
		next.ServeHTTP(w, r)
	})
}

// generateRequestID creates a unique hex string for tracking requests
func generateRequestID() string {
	bytes := make([]byte, 8)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}
