package main

import (
	"log"
	"net/http"
	"os"
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

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
