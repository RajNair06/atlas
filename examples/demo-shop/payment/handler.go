package main

import (
	"log/slog"
	"net/http"
	"os"
	"time"
)

func handlePay(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Extract request ID from header (passed from checkout)
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = "unknown"
	}

	slog.Info("payment started", "request_id", requestID)

	delay := paymentDelay()
	time.Sleep(delay)

	slog.Info("payment charged", "request_id", requestID, "duration_ms", time.Since(start).Milliseconds(), "delay", delay)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"charged"}`))
}

func paymentDelay() time.Duration {
	if v := os.Getenv("PAYMENT_DELAY"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 50 * time.Millisecond
}
