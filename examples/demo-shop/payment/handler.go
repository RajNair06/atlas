package main

import (
	"log/slog"
	"net/http"
	"os"
	"time"
)

func handlePay(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	time.Sleep(paymentDelay())

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"charged"}`))

	slog.Info("payment charged",
		"duration_ms", time.Since(start).Milliseconds(),
		"delay", paymentDelay().String(),
	)
}

func paymentDelay() time.Duration {
	if v := os.Getenv("PAYMENT_DELAY"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 50 * time.Millisecond
}
