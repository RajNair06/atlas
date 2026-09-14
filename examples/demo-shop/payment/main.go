package main

import (
	"log"
	"log/slog"
	"net/http"
	"os"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "payment")
	slog.SetDefault(logger)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /pay", handlePay)

	addr := ":" + envOr("PAYMENT_PORT", "8082")
	slog.Info("payment listening", "port", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
