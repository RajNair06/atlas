package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type server struct {
	checkoutURL string
	client      *http.Client
}

func (s *server) handleBuy(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	resp, err := s.client.Post(s.checkoutURL+"/checkout", "application/json", nil)
	if err != nil {
		slog.Error("checkout unreachable", "err", err.Error(), "duration_ms", time.Since(start).Milliseconds())
		http.Error(w, fmt.Sprintf("checkout unreachable: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Error("order failed", "status", resp.StatusCode, "duration_ms", time.Since(start).Milliseconds())
		http.Error(w, "order failed", resp.StatusCode)
		return
	}

	elapsed := time.Since(start).Round(time.Millisecond)
	slog.Info("order placed", "duration_ms", elapsed.Milliseconds(), "checkout_status", resp.StatusCode)
	fmt.Fprintf(w, "order placed in %s\n", elapsed)
}
