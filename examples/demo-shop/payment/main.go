package main

import (
	"log"
	"net/http"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /pay", handlePay)
	log.Fatal(http.ListenAndServe(":8082", mux))
}

func handlePay(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"charged"}`))
}
