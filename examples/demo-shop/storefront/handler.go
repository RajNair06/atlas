package main

import (
	"fmt"
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
		http.Error(w, fmt.Sprintf("checkout unreachable: %v", err), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, "order failed", resp.StatusCode)
		return
	}

	_, _ = fmt.Fprintf(w, "order placed in %s\n", time.Since(start).Round(time.Millisecond))
}
