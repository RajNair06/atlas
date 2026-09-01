package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/RajNair06/atlas/examples/demo-shop/otelsetup"
)

type server struct {
	checkoutURL string
	client      *http.Client
}

func (s *server) handleBuy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.checkoutURL+"/checkout", nil)
	if err != nil {
		http.Error(w, "building checkout request", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		otelsetup.Error(ctx, "checkout unreachable", "err", err)
		http.Error(w, fmt.Sprintf("checkout unreachable: %v", err), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		otelsetup.Error(ctx, "order failed", "status", resp.StatusCode)
		http.Error(w, "order failed", resp.StatusCode)
		return
	}

	elapsed := time.Since(start).Round(time.Millisecond)
	otelsetup.Info(ctx, "order placed", "duration_ms", elapsed.Milliseconds())
	_, _ = fmt.Fprintf(w, "order placed in %s\n", elapsed)
}
