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

	resp, err := s.client.Post(s.paymentURL+"/pay", "application/json", nil)
	if err != nil {
		slog.Error("payment unreachable", "err", err.Error(), "duration_ms", time.Since(start).Milliseconds())
		http.Error(w, fmt.Sprintf("payment unreachable: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Error("payment failed", "status", resp.StatusCode, "duration_ms", time.Since(start).Milliseconds())
		http.Error(w, "payment failed", http.StatusBadGateway)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Error("reading payment response", "err", err.Error())
		http.Error(w, "reading payment response", http.StatusBadGateway)
		return
	}

	slog.Info("checkout complete", "duration_ms", time.Since(start).Milliseconds(), "payment_status", resp.StatusCode)
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}
