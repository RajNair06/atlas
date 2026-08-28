package prober

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/RajNair06/atlas/internal/store"
)

type Result struct {
	Success    bool
	StatusCode int
	Latency    time.Duration
	Err        error
	Timestamp  time.Time
}

var defaultClient = &http.Client{
	Transport: &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	},
}

func Probe(ctx context.Context, m store.Monitor) Result {
	start := time.Now()
	res := Result{Timestamp: start}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.Target, nil)

	if err != nil {
		res.Err = err
		return res
	}

	resp, err := defaultClient.Do(req)
	res.Latency = time.Since(start)

	if err != nil {
		res.Err = err
		return res
	}

	defer resp.Body.Close()

	res.StatusCode = resp.StatusCode
	res.Success = resp.StatusCode < 400
	return res
}
