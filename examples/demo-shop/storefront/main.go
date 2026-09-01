package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/RajNair06/atlas/examples/demo-shop/otelsetup"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	shutdown, err := otelsetup.Setup(ctx, "storefront")
	if err != nil {
		log.Fatalf("otel setup: %v", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "storefront")
	slog.SetDefault(logger)

	s := &server{
		checkoutURL: envOr("CHECKOUT_URL", "http://localhost:8081"),
		client:      &http.Client{Transport: otelhttp.NewTransport(nil)},
	}

	addr := ":" + envOr("STOREFRONT_PORT", "8080")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /buy", s.handleBuy)

	srv := &http.Server{Addr: addr, Handler: otelhttp.NewHandler(mux, "storefront")}
	logger.Info("storefront listening", "addr", addr, "checkout_url", s.checkoutURL)

	if err := otelsetup.Serve(ctx, srv); err != nil {
		log.Fatalf("listen: %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = shutdown(shutdownCtx)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
