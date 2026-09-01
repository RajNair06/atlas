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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

type server struct {
	charged metric.Int64Counter
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	shutdown, err := otelsetup.Setup(ctx, "payment")
	if err != nil {
		log.Fatalf("otel setup: %v", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "payment")
	slog.SetDefault(logger)

	counter, err := otel.Meter("payment").Int64Counter("payment.charged")
	if err != nil {
		log.Fatalf("creating counter: %v", err)
	}

	s := &server{charged: counter}

	addr := ":" + envOr("PAYMENT_PORT", "8082")
	mux := http.NewServeMux()
	mux.HandleFunc("POST /pay", s.handlePay)

	srv := &http.Server{Addr: addr, Handler: otelhttp.NewHandler(mux, "payment")}
	logger.Info("payment listening", "addr", addr)

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
