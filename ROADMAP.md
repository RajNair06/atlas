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

#### Day 5 — Healing Actions ▶ (Part 1 done)
- ✅ Healing executor (`gateway/healing/executor.go`) with `RequestReplayer` interface (breaks the proxy↔healing import cycle)
- ✅ Retry with exponential backoff (100ms → 200ms → 400ms, max attempts from config)
- ✅ Synchronous auto-heal in the proxy: capture → Gemini → execute → healed response or original error (`auto_heal` config flag)
- ✅ `X-Healed` / `X-Healing-Action` / `X-Healing-Attempts` response headers
- ✅ Prompt tuned: connection-refused classified transient; fallback only when configured
- ✅ 5 executor unit tests (fake replayer) + live demos: give_up path, all-retries-fail path, heal-on-recovery path (200 + X-Healed: true)
- ⬜ Part 2: execute fallback action (replay against route fallback URL)
- ⬜ Part 2: circuit breaker per upstream (N consecutive failures → trip → fast-fail window)
- ⬜ Part 2: latency budget (cap total healing time per request)

#### Day 6 — Human-in-the-loop UI
- HTML page showing pending healing decisions (Go templates)
- Each card shows: request context, Gemini suggestion, [Approve] [Reject] buttons
- SSE endpoint for live updates when new decisions arrive
- htmx for button interactions (no React/Vue)
- When approved: execute action, update UI with result
- When rejected: return original error to client
- **Demo:** Kill payment, `curl /buy` hangs (waiting for approval), UI shows pending decision, click Approve, gateway retries, curl completes

#### Day 7 — Testing + Refinement
- End-to-end tests for each action type
- Edge cases: Gemini timeout, circuit breaker trip, human rejection
- Clean up logging, add request/response body capture (small bodies only)
- **Demo:** Generate various failure scenarios, verify all paths work

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
