package main

import (
	"log"
	"net/http"
)

func main() {
	http.HandleFunc("/", hello)
	log.Fatal(http.ListenAndServe(":8082", nil))
}

func hello(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("hello"))
}
