package httpapi

import (
	"fmt"
	"net/http"

	"github.com/RajNair06/atlas/internal/store"
)

type API struct {
	store *store.Store
}

func New(st *store.Store, port int) *http.Server {
	api := &API{store: st}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)
	mux.HandleFunc("POST /api/monitors", api.createMonitor)
	mux.HandleFunc("GET /api/monitors", api.listMonitors)
	mux.HandleFunc("GET /api/monitors/{id}", api.getMonitor)
	mux.HandleFunc("DELETE /api/monitors/{id}", api.deleteMonitor)

	return &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}
}
