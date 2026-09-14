package main

import (
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /checkout", handleCheckout)

	addr := ":" + envOr("CHECKOUT_PORT", "8081")
	log.Printf("checkout listening on %s (payment at %s)", addr, paymentURL())
	log.Fatal(http.ListenAndServe(addr, mux))
}

func paymentURL() string {
	return envOr("PAYMENT_URL", "http://localhost:8082")
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
