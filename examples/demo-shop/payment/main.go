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
