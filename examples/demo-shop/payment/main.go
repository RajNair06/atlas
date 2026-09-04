package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	addr := ":" + envOr("PAYMENT_PORT", "8082")

	mux := http.NewServeMux()
	mux.HandleFunc("POST /pay", handlePay)

	log.Printf("payment listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
