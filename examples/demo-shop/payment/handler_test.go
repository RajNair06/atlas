package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHandlePay(t *testing.T) {
	t.Setenv("PAYMENT_DELAY", "1ms")

	start := time.Now()
	rec := httptest.NewRecorder()
	handlePay(rec, httptest.NewRequest(http.MethodPost, "/pay", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != `{"status":"charged"}` {
		t.Fatalf("body = %q, want charged response", got)
	}
	if elapsed := time.Since(start); elapsed < time.Millisecond {
		t.Fatalf("returned in %s — the delay was not applied", elapsed)
	}
}

func TestPaymentDelay(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want time.Duration
	}{
		{"unset falls back to default", "", 50 * time.Millisecond},
		{"garbage falls back to default", "banana", 50 * time.Millisecond},
		{"seconds parse", "3s", 3 * time.Second},
		{"milliseconds parse", "250ms", 250 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PAYMENT_DELAY", tt.env)
			if got := paymentDelay(); got != tt.want {
				t.Fatalf("paymentDelay() = %s, want %s", got, tt.want)
			}
		})
	}
}
