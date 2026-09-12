package main

import (
	"net/http"
	"os"
	"time"
)

func handlePay(w http.ResponseWriter, r *http.Request) {
	time.Sleep(paymentDelay())

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
