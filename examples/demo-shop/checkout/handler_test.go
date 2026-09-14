package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleCheckoutForwardsPayment(t *testing.T) {
	payment := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/pay" {
			t.Errorf("fake payment got unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"charged"}`))
	}))
	defer payment.Close()

	s := &server{paymentURL: payment.URL, client: payment.Client()}

	rec := httptest.NewRecorder()
	s.handleCheckout(rec, httptest.NewRequest(http.MethodPost, "/checkout", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != `{"status":"charged"}` {
		t.Fatalf("body = %q, want payment's JSON forwarded verbatim", got)
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

func TestHandleCheckoutPaymentFails(t *testing.T) {
	payment := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bank on fire", http.StatusInternalServerError)
	}))
	defer payment.Close()

	s := &server{paymentURL: payment.URL, client: payment.Client()}

	rec := httptest.NewRecorder()
	s.handleCheckout(rec, httptest.NewRequest(http.MethodPost, "/checkout", nil))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
}
