package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric/noop"
)

func newTestServer(t *testing.T) *server {
	t.Helper()
	counter, err := noop.NewMeterProvider().Meter("test").Int64Counter("payment.charged")
	if err != nil {
		t.Fatal(err)
	}
	return &server{charged: counter}
}

func TestHandlePay(t *testing.T) {
	t.Setenv("PAYMENT_DELAY", "1ms")
	s := newTestServer(t)

	start := time.Now()
	rec := httptest.NewRecorder()
	s.handlePay(rec, httptest.NewRequest(http.MethodPost, "/pay", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != `{"status":"charged"}` {
		t.Fatalf("body = %q, want charged response", got)
	}
	if elapsed := time.Since(start); elapsed < time.Millisecond {
		t.Fatalf("handler returned too fast (%s), delay was not applied", elapsed)
	}
}

func TestHandlePayUsesDefaultDelay(t *testing.T) {
	t.Setenv("PAYMENT_DELAY", "")
	s := newTestServer(t)

	rec := httptest.NewRecorder()
	s.handlePay(rec, httptest.NewRequest(http.MethodPost, "/pay", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}
