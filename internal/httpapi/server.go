package httpapi

import (
	"fmt"
	"net/http"
)

func New(port int) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)
	return &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: mux}
}
