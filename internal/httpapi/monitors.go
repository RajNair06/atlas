package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/RajNair06/atlas/internal/store"
)

type monitorPayload struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Target   string `json:"target"`
	Interval int    `json:"interval"`
	Enabled  *bool  `json:"enabled"`
}

func (a *API) createMonitor(w http.ResponseWriter, r *http.Request) {
	var p monitorPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if p.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if p.Type != "http" {
		writeError(w, http.StatusBadRequest, "type must be http")
		return
	}
	if !strings.HasPrefix(p.Target, "http://") && !strings.HasPrefix(p.Target, "https://") {
		writeError(w, http.StatusBadRequest, "target must be an http(s) URL")
		return
	}
	if p.Interval < 1 {
		writeError(w, http.StatusBadRequest, "interval must be at least 1 second")
		return
	}
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}

	m, err := a.store.CreateMonitor(r.Context(), p.Name, p.Type, p.Target, p.Interval, enabled)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create monitor")
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (a *API) listMonitors(w http.ResponseWriter, r *http.Request) {
	monitors, err := a.store.ListMonitors(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list monitors")
		return
	}
	writeJSON(w, http.StatusOK, monitors)
}

func (a *API) getMonitor(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid monitor id")
		return
	}
	m, err := a.store.GetMonitor(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "monitor not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not get monitor")
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (a *API) deleteMonitor(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid monitor id")
		return
	}
	err = a.store.DeleteMonitor(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "monitor not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete monitor")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
