package prober

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RajNair06/atlas/internal/store"
)

func monitorFor(target string) store.Monitor {
	return store.Monitor{Type: "http", Target: target}
}

func TestProbeSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	res := Probe(context.Background(), monitorFor(srv.URL))
	if !res.Success {
		t.Errorf("Success = false, want true; err=%v", res.Err)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", res.StatusCode)
	}
	if res.Latency <= 0 {
		t.Errorf("Latency = %v, want > 0", res.Latency)
	}
}

func TestProbeServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	res := Probe(context.Background(), monitorFor(srv.URL))
	if res.Success {
		t.Error("Success = true, want false for 500")
	}
	if res.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", res.StatusCode)
	}
}

func TestProbeNotFoundIsDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	res := Probe(context.Background(), monitorFor(srv.URL))
	if res.Success {
		t.Error("Success = true, want false for 404")
	}
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", res.StatusCode)
	}
}

func TestProbeRefusedConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	res := Probe(context.Background(), monitorFor("http://"+addr))
	if res.Success {
		t.Error("Success = true, want false for refused connection")
	}
	if res.Err == nil {
		t.Error("Err = nil, want connection refused error")
	}
}

func TestProbeCancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res := Probe(ctx, monitorFor(srv.URL))
	if res.Success {
		t.Error("Success = true, want false for cancelled context")
	}
	if res.Err == nil {
		t.Error("Err = nil, want context error")
	}
}

func TestProbeHangingServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	res := Probe(ctx, monitorFor("http://"+ln.Addr().String()))
	elapsed := time.Since(start)

	if res.Success {
		t.Error("Success = true, want false for hanging server")
	}
	if res.Err == nil {
		t.Error("Err = nil, want timeout error")
	}
	if elapsed > 2*time.Second {
		t.Errorf("probe took %v; hanging server must not block past the deadline", elapsed)
	}
}
