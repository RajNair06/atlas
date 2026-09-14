package main

import (
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	s := &server{
		paymentURL: envOr("PAYMENT_URL", "http://localhost:8082"),
		client:     &http.Client{Timeout: paymentTimeout()},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /checkout", s.handleCheckout)

	addr := ":" + envOr("CHECKOUT_PORT", "8081")
	log.Printf("checkout listening on %s (payment at %s)", addr, s.paymentURL)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func paymentTimeout() time.Duration {
	if v := os.Getenv("PAYMENT_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 2 * time.Second
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
