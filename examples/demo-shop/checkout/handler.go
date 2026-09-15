package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

type server struct {
	paymentURL string
	client     *http.Client
}

func (s *server) handleCheckout(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Extract request ID from header (passed from storefront)
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = "unknown"
	}

	slog.Info("checkout started", "request_id", requestID)

	// Create request to payment with request ID in header
	paymentReq, err := http.NewRequestWithContext(r.Context(), "POST", s.paymentURL+"/pay", nil)
	if err != nil {
		slog.Error("failed to create payment request", "request_id", requestID, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	paymentReq.Header.Set("X-Request-ID", requestID)

	resp, err := s.client.Do(paymentReq)
	if err != nil {
		slog.Error("payment unreachable", "request_id", requestID, "err", err, "duration_ms", time.Since(start).Milliseconds())
		http.Error(w, fmt.Sprintf("payment unreachable: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Error("payment failed", "request_id", requestID, "status", resp.StatusCode, "duration_ms", time.Since(start).Milliseconds())
		http.Error(w, "payment failed", http.StatusBadGateway)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Error("reading payment response", "request_id", requestID, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	slog.Info("checkout complete", "request_id", requestID, "duration_ms", time.Since(start).Milliseconds(), "payment_status", resp.StatusCode)
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}
