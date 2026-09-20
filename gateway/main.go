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

	"github.com/RajNair06/atlas/gateway/approval"
	"github.com/RajNair06/atlas/gateway/config"
	"github.com/RajNair06/atlas/gateway/healing"
	"github.com/RajNair06/atlas/gateway/llm"
	"github.com/RajNair06/atlas/gateway/proxy"
	"github.com/RajNair06/atlas/gateway/webui"
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

	// Create Gemini client
	geminiClient := llm.NewGeminiClient(
		cfg.LLM.APIKey,
		cfg.LLM.Model,
		cfg.LLM.Timeout,
	)

	// Create decision engine
	decisionEngine := healing.NewDecisionEngine(geminiClient)

	// Create gateway with decision engine
	gw := proxy.New(cfg, decisionEngine)

	// Approval store: the human-in-the-loop gate. It is always wired when
	// auto-healing is on; server.require_approval is only the INITIAL state
	// of its runtime toggle — operators flip it live from the console (/ui).
	approvalStore := approval.NewStore()
	approvalStore.SetEnabled(cfg.Server.RequireApproval)
	var approver healing.Approver
	if cfg.Server.AutoHeal {
		approver = approvalStore
	}
	switch {
	case cfg.Server.RequireApproval && cfg.Server.AutoHeal:
		slog.Info("approval gate enabled",
			"approval_timeout", cfg.Server.ApprovalTimeout.String(),
		)
	case cfg.Server.RequireApproval:
		slog.Warn("require_approval is set but auto_heal is disabled; approval gate is inactive")
	default:
		slog.Info("approval gate off at startup (toggle it live in the console)")
	}

	// Create healing executor and wire it back into the gateway.
	// The executor replays failed requests through the gateway itself,
	// so it can only be constructed after the gateway exists.
	executor := healing.NewExecutor(decisionEngine, gw, healing.ExecutorOptions{
		MaxAttempts:      cfg.LLM.MaxHealingAttempts,
		LatencyBudget:    cfg.Server.HealingBudget,
		BreakerThreshold: cfg.Server.BreakerThreshold,
		BreakerReset:     cfg.Server.BreakerReset,
		Approver:         approver,
		ApprovalTimeout:  cfg.Server.ApprovalTimeout,
	})
	gw.SetExecutor(executor)

	// Create healing handler
	healingHandler := healing.NewHealingHandler(gw.GetErrorStore(), decisionEngine)

	// Create the healing console (dashboard UI + SSE + decision API + settings)
	ui := webui.NewHandler(approvalStore, cfg.Server.AutoHeal)

	// Set up routing
	mux := http.NewServeMux()
	mux.Handle("/healing/", healingHandler)
	mux.Handle("/ui", ui)
	mux.Handle("/ui/", ui)
	mux.Handle("/", gw)

	// Wrap with correlation ID middleware
	handler := withCorrelationID(mux)

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
