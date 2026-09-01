package main

import (
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/RajNair06/atlas/examples/demo-shop/otelsetup"
)

func (s *server) handlePay(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	delay := paymentDelay()

	trace.SpanFromContext(ctx).SetAttributes(
		attribute.String("demo.payment.delay", delay.String()),
	)

	time.Sleep(delay)

	s.charged.Add(ctx, 1)
	otelsetup.Info(ctx, "payment charged", "delay", delay.String())

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"charged"}`))
}

func paymentDelay() time.Duration {
	if v := os.Getenv("PAYMENT_DELAY"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 50 * time.Millisecond
}
