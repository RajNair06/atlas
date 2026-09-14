package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleBuy(t *testing.T) {
	checkout := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/checkout" {
			t.Errorf("fake checkout got unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"status":"charged"}`))
	}))
	defer checkout.Close()

	s := &server{checkoutURL: checkout.URL, client: checkout.Client()}

	rec := httptest.NewRecorder()
	s.handleBuy(rec, httptest.NewRequest(http.MethodGet, "/buy", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.HasPrefix(rec.Body.String(), "order placed in ") {
		t.Fatalf("body = %q, want order confirmation", rec.Body.String())
	}
}

func TestHandleBuyCheckoutDown(t *testing.T) {
	s := &server{checkoutURL: "http://127.0.0.1:1", client: &http.Client{}}

	rec := httptest.NewRecorder()
	s.handleBuy(rec, httptest.NewRequest(http.MethodGet, "/buy", nil))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
}

func TestHandleBuyForwardsFailureStatus(t *testing.T) {
	checkout := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "payment failed", http.StatusBadGateway)
	}))
	defer checkout.Close()

	s := &server{checkoutURL: checkout.URL, client: checkout.Client()}

	rec := httptest.NewRecorder()
	s.handleBuy(rec, httptest.NewRequest(http.MethodGet, "/buy", nil))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d — storefront must pass through checkout's failure code", rec.Code, http.StatusBadGateway)
	}
}
