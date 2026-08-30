package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	addr := ":" + envOr("CHECKOUT_PORT", "8081")

	s := &server{
		paymentURL: envOr("PAYMENT_URL", "http://localhost:8082"),
		client:     &http.Client{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /checkout", s.handleCheckout)

	log.Printf("checkout listening on %s (payment at %s)", addr, s.paymentURL)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
