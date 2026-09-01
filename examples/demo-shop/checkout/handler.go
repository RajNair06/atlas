package main

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/RajNair06/atlas/examples/demo-shop/otelsetup"
)

type server struct {
	paymentURL string
	client     *http.Client
}

func (s *server) handleCheckout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.paymentURL+"/pay", nil)
	if err != nil {
		http.Error(w, "building payment request", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		otelsetup.Error(ctx, "payment unreachable", "err", err)
		http.Error(w, fmt.Sprintf("payment unreachable: %v", err), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		otelsetup.Error(ctx, "payment failed", "status", resp.StatusCode)
		http.Error(w, "payment failed", http.StatusBadGateway)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "reading payment response", http.StatusBadGateway)
		return
	}

	otelsetup.Info(ctx, "checkout complete", "duration_ms", time.Since(start).Milliseconds())
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}
