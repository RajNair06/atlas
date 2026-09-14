package main

import (
	"fmt"
	"io"
	"net/http"
)

type server struct {
	paymentURL string
	client     *http.Client
}

func (s *server) handleCheckout(w http.ResponseWriter, r *http.Request) {
	resp, err := s.client.Post(s.paymentURL+"/pay", "application/json", nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("payment unreachable: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, "payment failed", http.StatusBadGateway)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "reading payment response", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}
