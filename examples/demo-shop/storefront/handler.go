package main

import (
	"fmt"
	"net/http"
	"time"
)

func handleBuy(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	client := &http.Client{Timeout: checkoutTimeout()}
	resp, err := client.Post(checkoutURL()+"/checkout", "application/json", nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("checkout unreachable: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, "order failed", resp.StatusCode)
		return
	}

	elapsed := time.Since(start).Round(time.Millisecond)
	fmt.Fprintf(w, "order placed in %s\n", elapsed)
}
