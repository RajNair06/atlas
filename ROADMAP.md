# ROADMAP.md

**Temporary scaffold — delete or rewrite when the project is complete.**

## Project Pivot (September 2026)

We've pivoted from the 19-increment AI SRE platform to a focused 2-week project:

**Self-Healing API Gateway with LLM-Powered Error Resolution**

A lightweight reverse proxy that intercepts 4xx/5xx errors, feeds them to Gemini, and lets a human approve healing actions (retry, fallback, give up).

**Why the pivot:**
- Tighter scope = shippable in 2 weeks vs 5-6 months
- Focuses on the most impressive features: reverse proxy, LLM tool-use, human-in-the-loop
- Reuses I1/I2 foundations (structured logging, correlation IDs, demo-shop services)
- Demonstrates real backend engineering + cloud deployment

---

## 2-Week Timeline

**Legend:** ✅ done · ▶ current · (rest = planned)

### Week 1: Gateway Core + LLM Integration

#### Day 1 — Reverse Proxy Foundation ✅
- Parse `gateway.yaml` (YAML config with routes, timeouts, retries)
- Build reverse proxy that routes requests to upstream services
- Structured logging with correlation IDs (reuse I2 patterns)
- `make gateway` target to run gateway + demo-shop together
- **Simplified design**: Exact path matching only, request path appended as-is to upstream URL
- **Demo:** `curl http://localhost:8080/buy` routes to storefront ✅

#### Day 2 — Demo-Shop Integration ✅
- Shift demo-shop ports (storefront 8081, checkout 8082, payment 8083)
- Gateway config routes `/buy` → storefront, `/checkout` → checkout, `/pay` → payment
- Verify all three routes work
- **Demo:** Full chain through gateway (gateway → storefront → checkout → payment)
- Integration test script added (`test-integration.sh`)

#### Day 3 — Error Interception ✅
- Wrapper around reverse proxy that captures responses
- On 4xx/5xx, capture full context: request, response, upstream, timing, correlation ID
- Store failed requests in-memory map (key = request ID)
- Structured log entry for each failure
- **Demo:** Kill payment, see detailed failure log with all context ✅

#### Day 4 — Gemini Integration ✅
- Gemini API client with HTTP calls to generativelanguage.googleapis.com
- Structured prompt template with full error context (method, path, status, headers, body)
- Response parsing and validation (action: retry/fallback/give_up)
- Healing endpoint at `/healing/{request_id}` for on-demand analysis
- Using model: `gemini-3.5-flash-lite`
- **Demo:** Kill payment, trigger error, call healing endpoint, get Gemini's structured suggestion ✅
- Added demo scripts (start-demo.sh, stop-demo.sh, demo-healing.sh) for easy testing

#### Day 5 — Healing Actions ✅
- ✅ Healing executor (`gateway/healing/executor.go`) with `RequestReplayer` + `Analyzer` interfaces (breaks the proxy↔healing import cycle, enables fake-based tests)
- ✅ Retry with exponential backoff (100ms → 200ms → 400ms, max attempts from config)
- ✅ Synchronous auto-heal in the proxy: capture → Gemini → execute → healed response or original error (`auto_heal` config flag)
- ✅ `X-Healed` / `X-Healing-Action` / `X-Healing-Attempts` response headers
- ✅ Prompt tuned: connection-refused classified transient; fallback only when configured
- ✅ Fallback execution: replay against route's fallback URL; plus last-resort chain (retries exhausted + fallback configured + budget left → try fallback)
- ✅ Transport-level failures (connection refused at the gateway) now captured and healed — previously invisible
- ✅ Circuit breaker per upstream (`breaker.go`): closed → open (threshold) → half-open (reset window, one trial) → closed/re-trip
- ✅ Latency budget: hard cap on total healing time per request (Gemini + retries + fallback)
- ✅ 28 unit tests in healing package (fakes for replayer + analyzer; breaker state machine; budget; chain)
- ✅ Live demos: give_up path · retry-all-fail · heal-on-recovery (X-Healed: true) · fallback chain (200 via backup) · breaker trip (2s → 2.4ms fast-fail, ~900×) · half-open trial + re-trip

#### Day 6 — Human-in-the-loop UI ✅
- ✅ Approval gate (`server.require_approval` + `server.approval_timeout`): after Gemini's suggestion, healing blocks until a human approves or rejects it; rejected → "rejected by operator: <reason>" → original error to client; timeout → decision marked expired → original error
- ✅ `healing.Approver` interface + thread-safe approval store (`gateway/approval`): pending map with buffered decision channels, timer-based expiry, 50-entry in-memory history (healed / failed / rejected / expired with attempts + durations), non-blocking SSE event feed (slow subscribers dropped)
- ✅ Latency-budget deadline now starts AFTER approval is granted — human think time never eats the machine budget (ungated behavior byte-for-byte unchanged)
- ✅ Dark "healing console" dashboard (`gateway/webui`, Go templates + htmx + Tailwind CDN, no build step): pending cards with live waiting timers, history feed, stat chips, status pills, ambient layered design, aria-live regions
- ✅ Endpoints: `GET /ui` · `GET /ui/events` (SSE) · `GET /ui/decisions` · `GET /ui/history` · `POST /ui/decisions/{id}/approve|reject` (200 `{"ok":true}` / 404 already-decided)
- ✅ Tests: gate semantics (approved executes, rejected/expired replay zero times, budget starts post-approval), store (concurrent Decide single-winner, history cap, subscriber back-pressure), webui handlers + SSE — all green under `-race`
- ✅ `scripts/demo-approval.sh`: kill payment → curl hangs → pending appears → restart payment → approve via API → curl completes with `X-Healed: true` → history shows healed
- **Demo:** Kill payment, `curl -X POST :8080/checkout` hangs, browser at `:8080/ui` shows the pending card, click Approve, curl completes healed ✅

#### Day 7 — Testing + Refinement ✅
- ✅ Test suite grew 65 → 114 top-level tests (config validation matrix alone adds 16 subtests); full `-race` suite runs in ~23s
- ✅ Gemini client fully unit-tested with httptest fakes (`gateway/llm/gemini_test.go`, 12 tests): exact request JSON shape, fence stripping, HTTP/HTML/malformed/empty-candidates errors, client timeout, API-key redaction, empty-key short-circuit — no real API calls in tests; baseURL made injectable via `NewGeminiClientWithBaseURL`
- ✅ Full-stack proxy E2E suite (`gateway/proxy/proxy_test.go`, 18 tests) driving the real `Gateway.ServeHTTP` + real executor + real replay through httptest upstreams and a fake analyzer: happy-path passthrough, request-ID propagation, 404, 5xx→retry heal (X-Healed headers), transport failure→recovery heal, give_up and analyzer-error passthrough, fallback heal, approval gate approved/rejected/expired (real approval store, no sleeps), skip_healing (response + transport), breaker fast-fail + half-open trial, POST body integrity (forwarded AND captured), auto_heal off (captures but never heals)
- ✅ Body capture cap: stored `RequestBody`/`ErrorBody` truncated at 4 KiB with `...[truncated]` marker (UTF-8-safe); client/upstream forwarding stays uncapped — proven by the 10 KiB body test
- ✅ Config tests (`gateway/config/config_test.go`): full round-trip, `${VAR}` substitution (set/empty/unset), 16-case validation matrix, malformed YAML, missing file; error-store tests: `-race` concurrency stress + `IsFailure` boundary table + truncation
- ✅ Executor backoff base delay now injectable (`ExecutorOptions.RetryBaseDelay`, default unchanged 100ms) so retry tests run in milliseconds
- ✅ CI hardening: `go vet` step added, tests now run with `-race`; `scripts/test-all.sh` = build + vet + race tests + coverage summary (proxy 92%, llm 90%, config 100%, errors 100%)
- **Demo:** `scripts/demo-approval.sh` re-run live after the refactor — pending → approve → `X-Healed: true` ✅

---

### Week 2: Observability + Deployment + Polish

#### Day 8 — Prometheus Metrics
- `/metrics` endpoint exposing:
  - `gateway_requests_total{path, status}`
  - `gateway_errors_total{path, status}`
  - `gateway_healing_attempts_total{action, result}`
  - `gateway_healing_duration_seconds` (histogram)
  - `gateway_llm_calls_total{result}`
  - `gateway_llm_cost_dollars` (estimated from tokens)
- Structured logs for every decision (suggested, approved, result)
- **Demo:** Generate traffic, see metrics in `/metrics`

#### Day 9 — Containerization
- Multi-stage Dockerfile (builder + minimal runtime)
- `docker-compose.yml` with gateway + demo-shop services
- Environment variable config (`GEMINI_API_KEY`, upstream URLs)
- Health check endpoint (`/health`)
- **Demo:** `docker-compose up`, everything runs in containers

#### Day 10 — Cloud Deployment
- Deploy to Railway or Fly.io (one-command deploy)
- Configure env vars in dashboard
- Verify gateway accessible from internet
- **Demo:** `curl https://your-gateway.railway.app/buy` works from anywhere

#### Day 11 — README + Documentation
- Architecture diagram (ASCII art)
- Quickstart: `git clone && cp gateway.yaml.example gateway.yaml && make demo`
- How healing works (flow diagram)
- Configuration reference (every field explained)
- Deployment guide (local + cloud)
- **Demo:** Follow quickstart on clean machine, works end-to-end

#### Day 12 — Demo Video/GIF
- Record full loop:
  1. Request fails (kill payment)
  2. Gemini suggests retry
  3. UI shows pending decision
  4. Human approves
  5. Gateway retries 2x
  6. Payment comes back, request succeeds
  7. Client gets success response
- Embed in README
- **Demo:** Watch video, understand the full loop in 2 minutes

#### Day 13 — Edge Cases + Polish
- Request body capture (JSON only, < 1KB)
- Response body capture in error cases
- Better error messages when config is invalid
- Graceful shutdown (finish in-flight healing before exit)
- **Demo:** Test malformed config, see helpful errors

#### Day 14 — Final Testing + Ship
- End-to-end test suite (happy path, retry success, retry failure, fallback, circuit breaker)
- Verify fresh-clone rule: clone, `make demo`, works
- Tag v0.1.0
- Write blog post / Twitter thread
- **Demo:** Public launch, shareable link, 2-minute video

---

## Completed Foundation Work

The following work from the original I1/I2 plan is **kept and reused**:

### I1 — Naive Systems & Felt Pain ✅
All 14 rungs complete. Provides:
- Demo-shop services (storefront, checkout, payment) as upstream services
- Understanding of cascading failures, timeouts, testing
- Makefile, CI, cascade-failure lab

### I2 — Observability (Partial) ✅
Rungs 1-3 complete. Provides:
- Structured logging with slog JSON
- Correlation IDs that ride in headers
- Foundation for gateway observability

---

## Technical Decisions

**Gateway architecture:**
- Reverse proxy pattern (client → gateway → upstream)
- Human-in-the-loop for all healing actions
- Containerized deployment (Railway/Fly.io)
- Gemini API for LLM decisions
- Exact route matching + basic prefix stripping
- Config reload requires restart (no hot reload)

**What we're NOT building:**
- Advanced LLM actions (param correction, body rewriting)
- Production hardening (rate limiting, auth, TLS termination)
- Persistent storage (everything in-memory)
- Sophisticated UI (functional HTML+htmx, not SPA)
- Multi-tenant (single config, not per-tenant rules)
- Auto-apply mode (all healing requires approval)

---

## Standing Rules (see LEARNING.md)

- One day at a time; every line of code explained; every command explained before it runs
- Each day ends with a live demo you can re-run yourself
- Commit per day (plain sentence), push at session end
- Side quests capped at 2 hours; every detour gets a one-paragraph write-up
- The 14-day timeline is the contract; daily deliverables are refined as we arrive
