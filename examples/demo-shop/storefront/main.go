package main

import (
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "storefront")
	slog.SetDefault(logger)

	s := &server{
		checkoutURL: envOr("CHECKOUT_URL", "http://localhost:8081"),
		client:      &http.Client{Timeout: checkoutTimeout()},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /buy", s.handleBuy)

	addr := ":" + envOr("STOREFRONT_PORT", "8080")
	slog.Info("storefront listening", "port", addr, "checkout_url", s.checkoutURL)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func checkoutTimeout() time.Duration {
	if v := os.Getenv("CHECKOUT_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 5 * time.Second
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
