package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	addr := ":" + envOr("STOREFRONT_PORT", "8080")

	s := &server{
		checkoutURL: envOr("CHECKOUT_URL", "http://localhost:8081"),
		client:      &http.Client{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /buy", s.handleBuy)

	log.Printf("storefront listening on %s (checkout at %s)", addr, s.checkoutURL)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
