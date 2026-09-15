package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

type server struct {
	checkoutURL string
	client      *http.Client
}

// generateRequestID creates a unique hex string for tracking requests
func generateRequestID() string {
	bytes := make([]byte, 8)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func (s *server) handleBuy(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Generate unique request ID at the edge
	requestID := generateRequestID()

	// Log with request ID
	slog.Info("buy request started", "request_id", requestID)

	// Create request to checkout with request ID in header
	checkoutReq, err := http.NewRequestWithContext(r.Context(), "POST", s.checkoutURL+"/checkout", nil)
	if err != nil {
		slog.Error("failed to create checkout request", "request_id", requestID, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	checkoutReq.Header.Set("X-Request-ID", requestID)

	resp, err := s.client.Do(checkoutReq)
	if err != nil {
		slog.Error("checkout unreachable", "request_id", requestID, "err", err, "duration_ms", time.Since(start).Milliseconds())
		http.Error(w, fmt.Sprintf("checkout unreachable: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Error("order failed", "request_id", requestID, "status", resp.StatusCode, "duration_ms", time.Since(start).Milliseconds())
		http.Error(w, "order failed", resp.StatusCode)
		return
	}

	_, err = io.ReadAll(resp.Body)
	if err != nil {
		slog.Error("reading checkout response", "request_id", requestID, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	elapsed := time.Since(start).Round(time.Millisecond)
	slog.Info("order placed", "request_id", requestID, "duration_ms", elapsed.Milliseconds(), "checkout_status", resp.StatusCode)
	fmt.Fprintf(w, "order placed in %s\n", elapsed)
}
