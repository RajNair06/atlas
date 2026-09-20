package proxy

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RajNair06/atlas/gateway/approval"
	"github.com/RajNair06/atlas/gateway/config"
	"github.com/RajNair06/atlas/gateway/errors"
	"github.com/RajNair06/atlas/gateway/healing"
)

// These tests drive the REAL Gateway.ServeHTTP through an httptest server:
// real HTTP, real routing, real error capture, real healing executor, real
// ReplayRequest. Only the two things that must never be real in a test are
// faked: the LLM (fakeAnalyzer) and the upstream services (httptest servers).

// --- fakes ---------------------------------------------------------------

// fakeAnalyzer returns a canned suggestion without calling any LLM.
// onAnalyze runs just before the suggestion is returned — the transport
// failure test uses it to "recover" the dead upstream at a deterministic
// point (after the failure was captured, before the first replay).
type fakeAnalyzer struct {
	suggestion *healing.HealingSuggestion
	err        error
	onAnalyze  func()

	mu    sync.Mutex
	calls int
}

func (f *fakeAnalyzer) AnalyzeError(req *errors.FailedRequest) (*healing.HealingSuggestion, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.onAnalyze != nil {
		f.onAnalyze()
	}
	return f.suggestion, f.err
}

func (f *fakeAnalyzer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

var _ healing.Analyzer = (*fakeAnalyzer)(nil)

func retrySuggestion() *healing.HealingSuggestion {
	return &healing.HealingSuggestion{Action: healing.ActionRetry, Reasoning: "looks transient"}
}

func giveUpSuggestion() *healing.HealingSuggestion {
	return &healing.HealingSuggestion{Action: healing.ActionGiveUp, Reasoning: "permanent client error"}
}

func fallbackSuggestion() *healing.HealingSuggestion {
	return &healing.HealingSuggestion{Action: healing.ActionFallback, Reasoning: "primary is down", FallbackPath: "/buy"}
}

// flakyUpstream is a fake upstream service: it answers the first `failures`
// calls with failStatus/failBody, then succeeds with okBody. It records the
// body and headers of every request so tests can assert what the proxy
// forwarded (and what a healing replay re-sent).
type flakyUpstream struct {
	url        string
	failures   int
	failStatus int
	failBody   string
	okBody     string

	mu      sync.Mutex
	calls   int
	bodies  []string
	headers []http.Header
}

func newFlakyUpstream(t *testing.T, failures, failStatus int, failBody, okBody string) *flakyUpstream {
	t.Helper()
	f := &flakyUpstream{failures: failures, failStatus: failStatus, failBody: failBody, okBody: okBody}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	f.url = srv.URL
	return f
}

func (f *flakyUpstream) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	f.mu.Lock()
	f.calls++
	n := f.calls
	f.bodies = append(f.bodies, string(body))
	f.headers = append(f.headers, r.Header.Clone())
	f.mu.Unlock()

	w.Header().Set("Content-Type", "text/plain")
	if n <= f.failures {
		w.Header().Set("X-Upstream-Attempt", fmt.Sprint(n))
		w.WriteHeader(f.failStatus)
		fmt.Fprint(w, f.failBody)
		return
	}
	w.Header().Set("X-Upstream", "demo-shop")
	fmt.Fprint(w, f.okBody)
}

func (f *flakyUpstream) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// receivedBodies returns every request body the upstream saw, in order.
func (f *flakyUpstream) receivedBodies() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.bodies...)
}

// receivedHeader returns the headers of call number n (1-based).
func (f *flakyUpstream) receivedHeader(n int) http.Header {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.headers[n-1].Clone()
}

// deadUpstreamAddr reserves a loopback port, closes it, and returns its URL.
// Connections to it fail fast with "connection refused" — exactly the
// transport failure the proxy must capture. The returned start func rebinds
// the same address and serves handler on it, simulating a recovery.
func deadUpstreamAddr(t *testing.T, handler http.Handler) (url string, start func()) {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a loopback port: %v", err)
	}
	addr := l.Addr().String()
	l.Close() // the port is now dead: connect() → connection refused

	var once sync.Once
	var mu sync.Mutex
	var live net.Listener

	start = func() {
		once.Do(func() {
			nl, err := net.Listen("tcp", addr)
			if err != nil {
				// Extremely unlikely (the port was free microseconds ago),
				// but never Fatal from a request-handling goroutine.
				t.Errorf("rebinding dead upstream port %s: %v", addr, err)
				return
			}
			mu.Lock()
			live = nl
			mu.Unlock()
			// Serves in the background until the cleanup below closes the
			// listener; once.Do must return immediately so the analyzer
			// (and the healing flow waiting on it) proceeds.
			go func() { _ = http.Serve(nl, handler) }()
		})
	}

	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		if live != nil {
			live.Close()
		}
	})

	return "http://" + addr, start
}

// --- harness --------------------------------------------------------------

// harnessOptions configures the fake world around a real Gateway: routes,
// analyzer behavior, approval gate, breaker settings.
type harnessOptions struct {
	routes           []config.RouteConfig
	autoHeal         bool
	suggestion       *healing.HealingSuggestion // default: retry
	analyzerErr      error                      // analyzer fails instead of suggesting
	onAnalyze        func()                     // runs inside AnalyzeError
	approver         healing.Approver           // nil = no approval gate
	approvalTimeout  time.Duration              // default 5s
	breakerThreshold int                        // 0 = breaker disabled
	breakerReset     time.Duration              // default 1m
	maxAttempts      int                        // default 3
	healingBudget    time.Duration              // default 2s
}

// harness is a fully wired gateway: config → Gateway → real Executor
// (replayer = the Gateway itself, as in production) → httptest server.
type harness struct {
	gw       *Gateway
	srv      *httptest.Server
	analyzer *fakeAnalyzer
	client   *http.Client
}

var requestIDCounter atomic.Uint64

// withRequestID mirrors main.go's correlation middleware: propagate a
// client-supplied X-Request-ID or generate one, and put it on the response.
// The proxy reads the request ID from this header, so E2E tests must run
// behind the same middleware production uses.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = fmt.Sprintf("test-%d", requestIDCounter.Add(1))
		}
		r.Header.Set("X-Request-ID", id)
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r)
	})
}

func newHarness(t *testing.T, opts harnessOptions) *harness {
	t.Helper()

	if opts.suggestion == nil && opts.analyzerErr == nil {
		opts.suggestion = retrySuggestion()
	}
	if opts.maxAttempts == 0 {
		opts.maxAttempts = 3
	}
	if opts.healingBudget == 0 {
		opts.healingBudget = 2 * time.Second
	}
	if opts.approvalTimeout == 0 {
		opts.approvalTimeout = 5 * time.Second
	}
	if opts.breakerReset == 0 {
		opts.breakerReset = time.Minute
	}

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:             8080,
			AutoHeal:         opts.autoHeal,
			RequireApproval:  opts.approver != nil,
			ApprovalTimeout:  opts.approvalTimeout,
			HealingBudget:    opts.healingBudget,
			BreakerThreshold: opts.breakerThreshold,
			BreakerReset:     opts.breakerReset,
		},
		LLM: config.LLMConfig{
			Provider:           "gemini",
			Model:              "fake-model",
			APIKey:             "test-key",
			Timeout:            time.Second,
			MaxHealingAttempts: opts.maxAttempts,
		},
		Routes: opts.routes,
	}

	analyzer := &fakeAnalyzer{
		suggestion: opts.suggestion,
		err:        opts.analyzerErr,
		onAnalyze:  opts.onAnalyze,
	}

	// The production wiring: gateway replays its own captured requests.
	gw := New(cfg, nil)
	executor := healing.NewExecutor(analyzer, gw, healing.ExecutorOptions{
		MaxAttempts:      cfg.LLM.MaxHealingAttempts,
		LatencyBudget:    cfg.Server.HealingBudget,
		BreakerThreshold: cfg.Server.BreakerThreshold,
		BreakerReset:     cfg.Server.BreakerReset,
		Approver:         opts.approver,
		ApprovalTimeout:  cfg.Server.ApprovalTimeout,
		RetryBaseDelay:   2 * time.Millisecond, // tiny backoff keeps tests fast
	})
	gw.SetExecutor(executor)

	srv := httptest.NewServer(withRequestID(gw))
	t.Cleanup(srv.Close)

	return &harness{gw: gw, srv: srv, analyzer: analyzer, client: srv.Client()}
}

func testRoute(path, upstream string) config.RouteConfig {
	return config.RouteConfig{Path: path, Upstream: upstream, Timeout: 2 * time.Second}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}
	return string(b)
}

// storedFailures returns everything the gateway captured in its error store.
func (h *harness) storedFailures() []*errors.FailedRequest {
	return h.gw.GetErrorStore().List()
}

// --- happy paths -----------------------------------------------------------

func TestProxyHappyPathPassthrough(t *testing.T) {
	up := newFlakyUpstream(t, 0, 0, "", `{"item":"book"}`)
	h := newHarness(t, harnessOptions{
		routes:   []config.RouteConfig{testRoute("/buy", up.url)},
		autoHeal: true,
	})

	resp, err := h.client.Get(h.srv.URL + "/buy")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if body != `{"item":"book"}` {
		t.Fatalf("body = %q, want the upstream's JSON", body)
	}
	// Upstream response headers are forwarded to the client.
	if got := resp.Header.Get("X-Upstream"); got != "demo-shop" {
		t.Fatalf("X-Upstream = %q, want demo-shop (upstream headers must pass through)", got)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/plain" {
		t.Fatalf("Content-Type = %q, want text/plain", got)
	}
	// Correlation ID middleware put a request ID on the response.
	if resp.Header.Get("X-Request-ID") == "" {
		t.Fatal("response has no X-Request-ID")
	}
	// Success must not touch healing at all.
	if resp.Header.Get("X-Healed") != "" {
		t.Fatal("successful request carries X-Healed")
	}
	if h.analyzer.callCount() != 0 {
		t.Fatalf("analyzer called %d times, want 0 on success", h.analyzer.callCount())
	}
	if n := len(h.storedFailures()); n != 0 {
		t.Fatalf("error store holds %d entries, want 0 on success", n)
	}
	if up.callCount() != 1 {
		t.Fatalf("upstream called %d times, want 1", up.callCount())
	}
}

func TestProxyRequestIDPropagation(t *testing.T) {
	up := newFlakyUpstream(t, 0, 0, "", "ok")
	h := newHarness(t, harnessOptions{
		routes:   []config.RouteConfig{testRoute("/buy", up.url)},
		autoHeal: true,
	})

	// A client-supplied ID is kept end to end.
	req, _ := http.NewRequest(http.MethodGet, h.srv.URL+"/buy", nil)
	req.Header.Set("X-Request-ID", "custom-42")
	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	readBody(t, resp)

	if got := resp.Header.Get("X-Request-ID"); got != "custom-42" {
		t.Fatalf("response X-Request-ID = %q, want custom-42", got)
	}
	if got := up.receivedHeader(1).Get("X-Request-ID"); got != "custom-42" {
		t.Fatalf("upstream saw X-Request-ID = %q, want custom-42", got)
	}

	// Without one, the middleware generates a unique ID per request.
	resp1, err := h.client.Get(h.srv.URL + "/buy")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	readBody(t, resp1)
	resp2, err := h.client.Get(h.srv.URL + "/buy")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	readBody(t, resp2)

	id1, id2 := resp1.Header.Get("X-Request-ID"), resp2.Header.Get("X-Request-ID")
	if id1 == "" || id2 == "" || id1 == id2 {
		t.Fatalf("generated request IDs = %q and %q, want two distinct non-empty IDs", id1, id2)
	}
}

func TestProxyUnknownRoute404(t *testing.T) {
	up := newFlakyUpstream(t, 0, 0, "", "ok")
	h := newHarness(t, harnessOptions{
		routes:   []config.RouteConfig{testRoute("/buy", up.url)},
		autoHeal: true,
	})

	resp, err := h.client.Get(h.srv.URL + "/not-a-route")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	readBody(t, resp)

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if up.callCount() != 0 {
		t.Fatal("unknown route reached an upstream")
	}
	if n := len(h.storedFailures()); n != 0 {
		t.Fatalf("error store holds %d entries, want 0 (404 at the gateway is not an upstream failure)", n)
	}
}

// --- healing through the proxy ---------------------------------------------

func TestProxyHealsUpstream5xxWithRetry(t *testing.T) {
	// First call 503, the healing replay succeeds.
	up := newFlakyUpstream(t, 1, http.StatusServiceUnavailable, "temporarily down", "recovered")
	h := newHarness(t, harnessOptions{
		routes:   []config.RouteConfig{testRoute("/buy", up.url)},
		autoHeal: true,
	})

	resp, err := h.client.Get(h.srv.URL + "/buy")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	body := readBody(t, resp)

	// The client sees the healed response, not the original 503.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want healed 200", resp.StatusCode)
	}
	if body != "recovered" {
		t.Fatalf("body = %q, want the replay's body", body)
	}
	if got := resp.Header.Get("X-Healed"); got != "true" {
		t.Fatalf("X-Healed = %q, want true", got)
	}
	if got := resp.Header.Get("X-Healing-Action"); got != "retry" {
		t.Fatalf("X-Healing-Action = %q, want retry", got)
	}
	if got := resp.Header.Get("X-Healing-Attempts"); got != "1" {
		t.Fatalf("X-Healing-Attempts = %q, want 1 (first replay succeeded)", got)
	}
	if resp.Header.Get("X-Request-ID") == "" {
		t.Fatal("healed response lost the correlation ID")
	}

	// The failure was captured before healing.
	stored := h.storedFailures()
	if len(stored) != 1 {
		t.Fatalf("error store holds %d entries, want 1", len(stored))
	}
	failed := stored[0]
	if failed.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("stored status = %d, want 503", failed.StatusCode)
	}
	if failed.ErrorBody != "temporarily down" {
		t.Errorf("stored error body = %q", failed.ErrorBody)
	}
	if failed.Path != "/buy" || failed.Method != http.MethodGet {
		t.Errorf("stored request = %s %s, want GET /buy", failed.Method, failed.Path)
	}
	if failed.RequestID != resp.Header.Get("X-Request-ID") {
		t.Errorf("stored request ID = %q, want the response's %q", failed.RequestID, resp.Header.Get("X-Request-ID"))
	}
	if failed.Upstream != up.url+"/buy" {
		t.Errorf("stored upstream = %q, want %q", failed.Upstream, up.url+"/buy")
	}

	if h.analyzer.callCount() != 1 {
		t.Fatalf("analyzer called %d times, want 1", h.analyzer.callCount())
	}
	if up.callCount() != 2 {
		t.Fatalf("upstream called %d times, want 2 (original + one replay)", up.callCount())
	}
}

func TestProxyHealsTransportFailureAfterUpstreamRecovers(t *testing.T) {
	// The upstream port is dead at request time (connection refused), then
	// "recovers" while the analyzer runs — before the first replay.
	upURL, startUpstream := deadUpstreamAddr(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "back online")
	}))

	h := newHarness(t, harnessOptions{
		routes:    []config.RouteConfig{testRoute("/buy", upURL)},
		autoHeal:  true,
		onAnalyze: startUpstream,
	})

	resp, err := h.client.Get(h.srv.URL + "/buy")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want healed 200", resp.StatusCode)
	}
	if body != "back online" {
		t.Fatalf("body = %q, want the recovered upstream's body", body)
	}
	if got := resp.Header.Get("X-Healed"); got != "true" {
		t.Fatalf("X-Healed = %q, want true", got)
	}

	// The transport failure was captured as a 502 with the dial error.
	stored := h.storedFailures()
	if len(stored) != 1 {
		t.Fatalf("error store holds %d entries, want 1", len(stored))
	}
	if stored[0].StatusCode != http.StatusBadGateway {
		t.Errorf("stored status = %d, want 502 for a transport failure", stored[0].StatusCode)
	}
	if !strings.Contains(stored[0].ErrorBody, "connection refused") {
		t.Errorf("stored error body = %q, want it to mention connection refused", stored[0].ErrorBody)
	}
}

func TestProxyGiveUpReturnsOriginalError(t *testing.T) {
	up := newFlakyUpstream(t, 100, http.StatusServiceUnavailable, "still broken", "never reached")
	h := newHarness(t, harnessOptions{
		routes:     []config.RouteConfig{testRoute("/buy", up.url)},
		autoHeal:   true,
		suggestion: giveUpSuggestion(),
	})

	resp, err := h.client.Get(h.srv.URL + "/buy")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	body := readBody(t, resp)

	// give_up → healing declines → the client sees the ORIGINAL failure.
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want the original 503", resp.StatusCode)
	}
	if body != "still broken" {
		t.Fatalf("body = %q, want the original upstream body", body)
	}
	if got := resp.Header.Get("X-Healed"); got != "" {
		t.Fatalf("X-Healed = %q, want it absent when healing declined", got)
	}
	if h.analyzer.callCount() != 1 {
		t.Fatalf("analyzer called %d times, want 1", h.analyzer.callCount())
	}
	if up.callCount() != 1 {
		t.Fatalf("upstream called %d times, want 1 (give_up must not replay)", up.callCount())
	}
	if n := len(h.storedFailures()); n != 1 {
		t.Fatalf("error store holds %d entries, want 1", n)
	}
}

func TestProxyAnalyzerErrorReturnsOriginalError(t *testing.T) {
	up := newFlakyUpstream(t, 100, http.StatusBadGateway, "bad gateway body", "never reached")
	h := newHarness(t, harnessOptions{
		routes:      []config.RouteConfig{testRoute("/buy", up.url)},
		autoHeal:    true,
		analyzerErr: fmt.Errorf("gemini unreachable"),
	})

	resp, err := h.client.Get(h.srv.URL + "/buy")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want the original 502", resp.StatusCode)
	}
	if body != "bad gateway body" {
		t.Fatalf("body = %q, want the original upstream body", body)
	}
	if resp.Header.Get("X-Healed") != "" {
		t.Fatal("X-Healed present although analysis failed")
	}
	if up.callCount() != 1 {
		t.Fatalf("upstream called %d times, want 1 (failed analysis must not replay)", up.callCount())
	}
}

func TestProxyHealsWithFallback(t *testing.T) {
	primary := newFlakyUpstream(t, 100, http.StatusServiceUnavailable, "primary down", "never reached")
	backup := newFlakyUpstream(t, 0, 0, "", "from backup")

	route := testRoute("/buy", primary.url)
	route.Fallback = backup.url

	h := newHarness(t, harnessOptions{
		routes:     []config.RouteConfig{route},
		autoHeal:   true,
		suggestion: fallbackSuggestion(),
	})

	resp, err := h.client.Get(h.srv.URL + "/buy")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want healed 200", resp.StatusCode)
	}
	if body != "from backup" {
		t.Fatalf("body = %q, want the backup's body", body)
	}
	if got := resp.Header.Get("X-Healing-Action"); got != "fallback" {
		t.Fatalf("X-Healing-Action = %q, want fallback", got)
	}
	if primary.callCount() != 1 {
		t.Fatalf("primary called %d times, want 1 (fallback must not retry it)", primary.callCount())
	}
	if backup.callCount() != 1 {
		t.Fatalf("backup called %d times, want 1", backup.callCount())
	}
	// The replay hit the fallback base + the original path.
	if got := backup.receivedHeader(1).Get("X-Request-ID"); got == "" {
		t.Fatal("fallback replay lost the correlation ID header")
	}
}

// --- approval gate through the proxy ----------------------------------------

// waitForPending polls the approval store until exactly one decision is
// pending and returns it. Polling (not sleeping) keeps this deterministic.
func waitForPending(t *testing.T, store *approval.Store) healing.PendingDecision {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pending := store.ListPending()
		if len(pending) == 1 {
			return pending[0]
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for a pending healing decision")
	return healing.PendingDecision{}
}

// runGatedRequest fires a request that will block on the approval gate,
// waits for the pending decision, decides it, and returns the response.
func runGatedRequest(t *testing.T, h *harness, store *approval.Store, approved bool, reason string) *http.Response {
	t.Helper()

	type result struct {
		resp *http.Response
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := h.client.Get(h.srv.URL + "/buy")
		done <- result{resp, err}
	}()

	pending := waitForPending(t, store)
	if pending.Action != healing.ActionRetry {
		t.Fatalf("pending action = %s, want retry", pending.Action)
	}
	if pending.Path != "/buy" {
		t.Fatalf("pending path = %q, want /buy", pending.Path)
	}
	if err := store.Decide(pending.ID, approved, reason); err != nil {
		t.Fatalf("deciding %s: %v", pending.ID, err)
	}

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("request failed: %v", r.err)
		}
		return r.resp
	case <-time.After(5 * time.Second):
		t.Fatal("request never completed after the decision")
		return nil
	}
}

func TestProxyApprovalGateApproved(t *testing.T) {
	up := newFlakyUpstream(t, 1, http.StatusServiceUnavailable, "temporarily down", "recovered")
	store := approval.NewStore()
	h := newHarness(t, harnessOptions{
		routes:   []config.RouteConfig{testRoute("/buy", up.url)},
		autoHeal: true,
		approver: store,
	})

	resp := runGatedRequest(t, h, store, true, "")
	body := readBody(t, resp)

	// Approved → the heal executes and the client sees the healed response.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want healed 200", resp.StatusCode)
	}
	if body != "recovered" {
		t.Fatalf("body = %q, want the replay's body", body)
	}
	if got := resp.Header.Get("X-Healed"); got != "true" {
		t.Fatalf("X-Healed = %q, want true", got)
	}

	// The console history shows a healed entry for this decision.
	history := store.ListHistory()
	if len(history) != 1 {
		t.Fatalf("history holds %d entries, want 1", len(history))
	}
	if history[0].Outcome != healing.OutcomeHealed {
		t.Fatalf("outcome = %s, want healed", history[0].Outcome)
	}
}

func TestProxyApprovalGateRejected(t *testing.T) {
	up := newFlakyUpstream(t, 100, http.StatusServiceUnavailable, "still broken", "never reached")
	store := approval.NewStore()
	h := newHarness(t, harnessOptions{
		routes:   []config.RouteConfig{testRoute("/buy", up.url)},
		autoHeal: true,
		approver: store,
	})

	resp := runGatedRequest(t, h, store, false, "deploy freeze in effect")
	body := readBody(t, resp)

	// Rejected → the client sees the ORIGINAL error, untouched.
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want the original 503", resp.StatusCode)
	}
	if body != "still broken" {
		t.Fatalf("body = %q, want the original upstream body", body)
	}
	if resp.Header.Get("X-Healed") != "" {
		t.Fatal("X-Healed present although the operator rejected")
	}
	// Rejection must not replay anything: the upstream saw exactly one call.
	if up.callCount() != 1 {
		t.Fatalf("upstream called %d times, want 1 (rejection replays nothing)", up.callCount())
	}

	history := store.ListHistory()
	if len(history) != 1 {
		t.Fatalf("history holds %d entries, want 1", len(history))
	}
	if history[0].Outcome != healing.OutcomeRejected {
		t.Fatalf("outcome = %s, want rejected", history[0].Outcome)
	}
	if history[0].Reason != "deploy freeze in effect" {
		t.Fatalf("reason = %q, want the operator's note", history[0].Reason)
	}
}

func TestProxyApprovalGateExpired(t *testing.T) {
	up := newFlakyUpstream(t, 100, http.StatusServiceUnavailable, "still broken", "never reached")
	store := approval.NewStore()
	h := newHarness(t, harnessOptions{
		routes:          []config.RouteConfig{testRoute("/buy", up.url)},
		autoHeal:        true,
		approver:        store,
		approvalTimeout: 30 * time.Millisecond, // expires long before any human
	})

	// Nobody decides: the gate must expire on its own and let the original
	// error through.
	resp, err := h.client.Get(h.srv.URL + "/buy")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want the original 503", resp.StatusCode)
	}
	if body != "still broken" {
		t.Fatalf("body = %q, want the original upstream body", body)
	}
	if resp.Header.Get("X-Healed") != "" {
		t.Fatal("X-Healed present although the decision expired")
	}
	if up.callCount() != 1 {
		t.Fatalf("upstream called %d times, want 1 (expiry replays nothing)", up.callCount())
	}

	history := store.ListHistory()
	if len(history) != 1 {
		t.Fatalf("history holds %d entries, want 1", len(history))
	}
	if history[0].Outcome != healing.OutcomeExpired {
		t.Fatalf("outcome = %s, want expired", history[0].Outcome)
	}
}

// --- skip_healing and auto_heal off -----------------------------------------

func TestProxySkipHealingPassesFailureThrough(t *testing.T) {
	up := newFlakyUpstream(t, 100, http.StatusServiceUnavailable, "unmonitored failure", "never reached")
	route := testRoute("/buy", up.url)
	route.SkipHealing = true

	h := newHarness(t, harnessOptions{
		routes:   []config.RouteConfig{route},
		autoHeal: true, // even with healing enabled, the route opts out
	})

	resp, err := h.client.Get(h.srv.URL + "/buy")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want the original 503", resp.StatusCode)
	}
	if body != "unmonitored failure" {
		t.Fatalf("body = %q, want the original upstream body", body)
	}
	if resp.Header.Get("X-Healed") != "" {
		t.Fatal("X-Healed present on a skip_healing route")
	}
	if h.analyzer.callCount() != 0 {
		t.Fatalf("analyzer called %d times, want 0 (skip_healing must not analyze)", h.analyzer.callCount())
	}
	if n := len(h.storedFailures()); n != 0 {
		t.Fatalf("error store holds %d entries, want 0 (skip_healing routes are not captured)", n)
	}
}

func TestProxySkipHealingTransportFailure(t *testing.T) {
	// A dead upstream on a skip_healing route: the client gets 502 and
	// nothing is captured or analyzed.
	upURL, _ := deadUpstreamAddr(t, http.NotFoundHandler())
	route := testRoute("/buy", upURL)
	route.SkipHealing = true

	h := newHarness(t, harnessOptions{
		routes:   []config.RouteConfig{route},
		autoHeal: true,
	})

	resp, err := h.client.Get(h.srv.URL + "/buy")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	if !strings.Contains(body, "upstream unavailable") {
		t.Fatalf("body = %q, want the gateway's transport-failure message", body)
	}
	if h.analyzer.callCount() != 0 {
		t.Fatalf("analyzer called %d times, want 0", h.analyzer.callCount())
	}
	if n := len(h.storedFailures()); n != 0 {
		t.Fatalf("error store holds %d entries, want 0", n)
	}
}

func TestProxyAutoHealDisabledStoresButNeverHeals(t *testing.T) {
	up := newFlakyUpstream(t, 100, http.StatusServiceUnavailable, "still broken", "never reached")
	h := newHarness(t, harnessOptions{
		routes:   []config.RouteConfig{testRoute("/buy", up.url)},
		autoHeal: false, // capture still happens; healing does not
	})

	resp, err := h.client.Get(h.srv.URL + "/buy")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want the original 503", resp.StatusCode)
	}
	if body != "still broken" {
		t.Fatalf("body = %q, want the original upstream body", body)
	}
	if resp.Header.Get("X-Healed") != "" {
		t.Fatal("X-Healed present although auto_heal is off")
	}
	if h.analyzer.callCount() != 0 {
		t.Fatalf("analyzer called %d times, want 0 with auto_heal off", h.analyzer.callCount())
	}
	if up.callCount() != 1 {
		t.Fatalf("upstream called %d times, want 1", up.callCount())
	}
	// Pinned behavior: with auto_heal off the failure IS still captured —
	// the store feeds the /healing/{id} endpoint, independent of auto-heal.
	stored := h.storedFailures()
	if len(stored) != 1 {
		t.Fatalf("error store holds %d entries, want 1 (capture is independent of auto_heal)", len(stored))
	}
	if stored[0].StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("stored status = %d, want 503", stored[0].StatusCode)
	}
}

// --- circuit breaker through the proxy --------------------------------------

func TestProxyBreakerFailsFastWithoutAnalyzer(t *testing.T) {
	up := newFlakyUpstream(t, 100, http.StatusServiceUnavailable, "still broken", "never reached")
	h := newHarness(t, harnessOptions{
		routes:           []config.RouteConfig{testRoute("/buy", up.url)},
		autoHeal:         true,
		breakerThreshold: 2,
		breakerReset:     time.Minute, // stays open for the whole test: no sleeps
		maxAttempts:      1,           // each heal gives up after one fast replay
	})

	// Requests 1 and 2 fail and heal-fail: each records one breaker failure,
	// so the circuit trips at the end of request 2.
	for i := 1; i <= 2; i++ {
		resp, err := h.client.Get(h.srv.URL + "/buy")
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		if code := readBody(t, resp); code != "still broken" || resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("request %d: status = %d, body = %q", i, resp.StatusCode, code)
		}
	}
	callsAfterTwo := h.analyzer.callCount()
	if callsAfterTwo != 2 {
		t.Fatalf("analyzer called %d times after two failures, want 2", callsAfterTwo)
	}

	// Request 3: circuit is open → healing fast-fails BEFORE the analyzer,
	// and the client still gets the original error.
	start := time.Now()
	resp, err := h.client.Get(h.srv.URL + "/buy")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("request 3 failed: %v", err)
	}
	body := readBody(t, resp)

	if resp.StatusCode != http.StatusServiceUnavailable || body != "still broken" {
		t.Fatalf("request 3: status = %d, body = %q, want the original 503", resp.StatusCode, body)
	}
	if h.analyzer.callCount() != callsAfterTwo {
		t.Fatalf("analyzer called %d times, want it to stay at %d (open circuit skips the LLM)",
			h.analyzer.callCount(), callsAfterTwo)
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("fast-failed request took %s, want no backoff sleeps", elapsed)
	}
	if n := len(h.storedFailures()); n != 3 {
		t.Fatalf("error store holds %d entries, want 3 (capture is independent of the breaker)", n)
	}
}

func TestProxyBreakerHalfOpenAdmitsOneTrial(t *testing.T) {
	up := newFlakyUpstream(t, 100, http.StatusServiceUnavailable, "still broken", "never reached")
	h := newHarness(t, harnessOptions{
		routes:           []config.RouteConfig{testRoute("/buy", up.url)},
		autoHeal:         true,
		breakerThreshold: 1,                    // trips on the very first failure
		breakerReset:     30 * time.Millisecond, // tiny window: one short sleep
		maxAttempts:      1,
	})

	// Request 1: failure trips the breaker (threshold 1).
	resp, err := h.client.Get(h.srv.URL + "/buy")
	if err != nil {
		t.Fatalf("request 1 failed: %v", err)
	}
	readBody(t, resp)
	if h.analyzer.callCount() != 1 {
		t.Fatalf("analyzer called %d times, want 1", h.analyzer.callCount())
	}

	// Request 2 (inside the reset window): open circuit fast-fails.
	resp, err = h.client.Get(h.srv.URL + "/buy")
	if err != nil {
		t.Fatalf("request 2 failed: %v", err)
	}
	readBody(t, resp)
	if h.analyzer.callCount() != 1 {
		t.Fatalf("analyzer called %d times, want it to stay at 1 while open", h.analyzer.callCount())
	}

	// Wait out the tiny reset window: the breaker goes half-open.
	time.Sleep(40 * time.Millisecond)

	// Request 3: half-open admits exactly one trial → analyzer runs again.
	// The trial fails (upstream still broken) → breaker re-opens.
	resp, err = h.client.Get(h.srv.URL + "/buy")
	if err != nil {
		t.Fatalf("request 3 failed: %v", err)
	}
	readBody(t, resp)
	if h.analyzer.callCount() != 2 {
		t.Fatalf("analyzer called %d times, want 2 (half-open trial)", h.analyzer.callCount())
	}

	// Request 4: re-opened circuit fast-fails again.
	resp, err = h.client.Get(h.srv.URL + "/buy")
	if err != nil {
		t.Fatalf("request 4 failed: %v", err)
	}
	readBody(t, resp)
	if h.analyzer.callCount() != 2 {
		t.Fatalf("analyzer called %d times, want it to stay at 2 after re-trip", h.analyzer.callCount())
	}
}

// --- body integrity and capture caps ----------------------------------------

func TestProxyPOSTBodyIntegrity(t *testing.T) {
	up := newFlakyUpstream(t, 1, http.StatusServiceUnavailable, "temporarily down", "order placed")
	h := newHarness(t, harnessOptions{
		routes:   []config.RouteConfig{testRoute("/checkout", up.url)},
		autoHeal: true,
	})

	const payload = `{"item":"book","qty":2,"gift":true}`

	resp, err := h.client.Post(h.srv.URL+"/checkout", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	body := readBody(t, resp)

	// Healed on the first replay: the replay must re-send the captured body.
	if resp.StatusCode != http.StatusOK || body != "order placed" {
		t.Fatalf("status = %d, body = %q, want healed 200", resp.StatusCode, body)
	}
	bodies := up.receivedBodies()
	if len(bodies) != 2 {
		t.Fatalf("upstream saw %d requests, want 2", len(bodies))
	}
	for i, got := range bodies {
		if got != payload {
			t.Fatalf("upstream request %d body = %q, want the exact client payload", i+1, got)
		}
	}

	// The captured failure kept the request body for the LLM prompt.
	stored := h.storedFailures()
	if len(stored) != 1 {
		t.Fatalf("error store holds %d entries, want 1", len(stored))
	}
	if stored[0].RequestBody != payload {
		t.Fatalf("stored request body = %q, want %q", stored[0].RequestBody, payload)
	}
	if stored[0].Method != http.MethodPost {
		t.Fatalf("stored method = %q, want POST", stored[0].Method)
	}
}

func TestProxyCapsCapturedBodiesButForwardsFully(t *testing.T) {
	// A 10 KiB request body and a 10 KiB error response: both far exceed the
	// 4 KiB capture cap, but both must still traverse the proxy in full.
	bigRequest := strings.Repeat("R", 10*1024)
	bigError := strings.Repeat("E", 10*1024)

	up := newFlakyUpstream(t, 100, http.StatusInternalServerError, bigError, "never reached")
	h := newHarness(t, harnessOptions{
		routes:     []config.RouteConfig{testRoute("/checkout", up.url)},
		autoHeal:   true,
		suggestion: giveUpSuggestion(), // no replay: keeps the test focused
	})

	resp, err := h.client.Post(h.srv.URL+"/checkout", "application/json", strings.NewReader(bigRequest))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	body := readBody(t, resp)

	// The client receives the FULL original error body (forwarding uncapped).
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	if body != bigError {
		t.Fatalf("client body length = %d, want the full %d bytes", len(body), len(bigError))
	}
	// The upstream received the FULL request body (forwarding uncapped).
	bodies := up.receivedBodies()
	if len(bodies) != 1 || bodies[0] != bigRequest {
		t.Fatalf("upstream body length = %d, want the full %d bytes", len(bodies[0]), len(bigRequest))
	}

	// The STORED copies are capped with the truncation marker.
	stored := h.storedFailures()
	if len(stored) != 1 {
		t.Fatalf("error store holds %d entries, want 1", len(stored))
	}
	assertTruncated := func(name, got string) {
		t.Helper()
		if !strings.HasSuffix(got, errors.TruncatedSuffix) {
			t.Fatalf("%s does not end with the truncation marker: len %d", name, len(got))
		}
		if len(got) != errors.MaxCapturedBody+len(errors.TruncatedSuffix) {
			t.Fatalf("%s length = %d, want cap + marker", name, len(got))
		}
	}
	assertTruncated("stored request body", stored[0].RequestBody)
	assertTruncated("stored error body", stored[0].ErrorBody)
	if !strings.HasPrefix(stored[0].RequestBody, "RRRR") || !strings.HasPrefix(stored[0].ErrorBody, "EEEE") {
		t.Fatal("truncated copies lost their leading bytes")
	}
}
