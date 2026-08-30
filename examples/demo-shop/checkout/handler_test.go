package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleCheckout(t *testing.T) {
	payment := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/pay" {
			t.Errorf("unexpected request to payment: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"charged"}`))
	}))
	defer payment.Close()

	s := &server{paymentURL: payment.URL, client: &http.Client{}}

	rec := httptest.NewRecorder()
	s.handleCheckout(rec, httptest.NewRequest(http.MethodPost, "/checkout", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != `{"status":"charged"}` {
		t.Fatalf("body = %q, want payment response forwarded", got)
	}
}

func TestHandleCheckoutPaymentDown(t *testing.T) {
	s := &server{paymentURL: "http://127.0.0.1:1", client: &http.Client{}}

	rec := httptest.NewRecorder()
	s.handleCheckout(rec, httptest.NewRequest(http.MethodPost, "/checkout", nil))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
}
