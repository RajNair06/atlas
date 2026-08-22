package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"github.com/RajNair06/atlas/internal/store"
)

func newTestAPI(t *testing.T) *API {
	t.Helper()
	dsn := os.Getenv("ATLAS_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://atlas:atlas@localhost:5433/atlas?sslmode=disable"
	}
	st, err := store.New(context.Background(), dsn)
	if err != nil {
		t.Skipf("database unreachable, skipping: %v", err)
	}
	t.Cleanup(st.Close)

	if _, err := st.Exec(context.Background(), "TRUNCATE monitors"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return &API{store: st}
}

func TestMonitorAPI(t *testing.T) {
	api := newTestAPI(t)
	mux := New(api.store, 0).Handler

	req := httptest.NewRequest("POST", "/api/monitors",
		bytes.NewBufferString(`{"name":"API","type":"http","target":"https://example.com","interval":60}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	var created store.Monitor
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.ID == 0 {
		t.Error("expected generated id")
	}

	req = httptest.NewRequest("GET", "/api/monitors/"+strconv.FormatInt(created.ID, 10), nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("get status = %d, want 200", w.Code)
	}

	req = httptest.NewRequest("DELETE", "/api/monitors/"+strconv.FormatInt(created.ID, 10), nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Errorf("delete status = %d, want 204", w.Code)
	}

	req = httptest.NewRequest("GET", "/api/monitors/"+strconv.FormatInt(created.ID, 10), nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("get after delete status = %d, want 404", w.Code)
	}
}

func TestCreateMonitorValidation(t *testing.T) {
	api := newTestAPI(t)
	mux := New(api.store, 0).Handler

	req := httptest.NewRequest("POST", "/api/monitors",
		bytes.NewBufferString(`{"name":"","type":"http","target":"https://example.com"}`))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty name status = %d, want 400", w.Code)
	}

	req = httptest.NewRequest("POST", "/api/monitors",
		bytes.NewBufferString(`{"name":"X","type":"http","target":"ftp://example.com"}`))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad scheme status = %d, want 400", w.Code)
	}
}
