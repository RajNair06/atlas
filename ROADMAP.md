# ROADMAP.md

**Temporary scaffold — delete or rewrite when the project is complete.**

The whole project, broken into rungs. A **rung** = one concept + one small change + one live demo. We never climb two rungs at once. Rungs below the current position are rough sketches; each gets refined into precise steps when we arrive.

**Legend:** ✅ done · ▶ current · (rest = planned)

---

## I1 — Naive systems & felt pain  ▶ (rebuilding from first principles)

1. ✅ What a server is (ports, listening, refused)
2. ✅ HTTP is text (requests, responses, methods, status codes)
3. ✅ First Go server (main, handlers, ListenAndServe, exit codes)
4. ✅ The handler dissected (w/r, pointers, mux routing, 404/405)
5. ✅ Packages & modules (folder=package, go.mod, import paths, visibility)
6. ✅ Environment variables as config (port from env, `time.Sleep`, the `PAYMENT_DELAY` chaos knob)
7. ✅ First client call — checkout is born (http.Client, calling another service)
8. ✅ The no-timeout trap (slow downstream hangs upstream; goroutines pile up)
9. ✅ Timeouts: what they fix, what they cost (fail-fast vs slow-but-successful)
10. ✅ The three-service chain (storefront; latency stacks; failure propagates)
11. ✅ Testing (httptest, fake downstreams, table-driven tests)
12. ✅ Makefile (targets, `-include .env`, why make exists)
13. ✅ CI (GitHub Actions YAML, green/red gates)
14. ✅ Cascade-failure lab (delay storm, kill payment, timeout experiment — the I1 aha)

---

## I2 — Observability ▶ (next increment)

1. Structured logging (why printf-logs die at scale; slog; key=value)
2. Logs are not enough (grep three services for one request — feel the pain)
3. Correlation IDs (a request ID that rides along in a header)
4. Traces: spans, parents, trees (the shape of one request)
5. Context as the carrier (`context.Context`, why every Go function takes it first)
6. OTel SDK by hand (create spans manually, see them work)
7. otelhttp middleware (automatic spans, the `traceparent` header on the wire)
8. Jaeger (read your first span tree in a UI)
9. RED metrics (counters, histograms, rate/errors/duration)
10. The OTel Collector (why a middleman; receivers → processors → exporters)
11. Goroutine trap lab (lose a trace across a goroutine, fix it)
12. Bug-hunt lab (logs-only vs traces: time both — the "20 min → 40 sec" artifact)

## I2 — Observability

1. Structured logging (why printf-logs die at scale; slog; key=value)
2. Logs are not enough (grep three services for one request — feel the pain)
3. Correlation IDs (a request ID that rides along in a header)
4. Traces: spans, parents, trees (the shape of one request)
5. Context as the carrier (`context.Context`, why every Go function takes it first)
6. OTel SDK by hand (create spans manually, see them work)
7. otelhttp middleware (automatic spans, the `traceparent` header on the wire)
8. Jaeger (read your first span tree in a UI)
9. RED metrics (counters, histograms, rate/errors/duration)
10. The OTel Collector (why a middleman; receivers → processors → exporters)
11. Goroutine trap lab (lose a trace across a goroutine, fix it)
12. Bug-hunt lab (logs-only vs traces: time both — the "20 min → 40 sec" artifact)

## I3 — Alert fatigue

1. Prometheus: pull-based metrics + PromQL basics
2. Alert rules: "page me when X"
3. Alertmanager: grouping, silences, what a page actually is
4. Loki (logs at scale) + Tempo (traces at scale, replaces Jaeger)
5. Grafana dashboards
6. Deliberately naive alerting (one alert per symptom, no grouping)
7. Storm lab: run chaos, count the pages, sit with the number
8. The BYO observability seam (every endpoint is just config)

## I4 — Alert gateway (first product service)

1. Alerts vs incidents (why 300 alerts ≠ 300 incidents)
2. Alertmanager webhooks (receive alerts over HTTP)
3. Idempotency & fingerprints (the same alert twice = one incident)
4. Redis (in-memory state; grouping windows)
5. PostgreSQL + migrations (durable state)
6. Grouping logic (storm → incidents)
7. Replay lab: 300 alerts → N incidents, tune the window

## I5 — Queues (no alert ever lost)

1. Pain first: kill the gateway mid-storm, count lost alerts
2. What a queue is and why HTTP push isn't enough
3. NATS JetStream basics
4. The queue interface (adapter #1: nats ↔ sqs)
5. Retries, backoff, dead-letter queues
6. Idempotent consumers
7. The delivery ledger ("where's my alert?")
8. Audit lab: 1,000 in, 1,000 out, 0 lost, 0 duplicated

## I6 — Incident service + first UI

1. The incident state machine (firing → acked → investigating → resolved)
2. Durable state survives crashes
3. SSE (server-sent events: pushing updates to a browser)
4. Go templates + htmx (HTML from the server, no build step)
5. Live incident feed
6. Crash/race labs; SSE reconnection
7. Fresh-clone checkpoint #1 (`make demo` on a virgin machine)

## I7 — Identity & auth

1. Pain first: curl in a fake alert, resolve a real incident
2. HMAC-signed webhooks (reject forgeries)
3. JWTs (tokens, claims, expiry)
4. Roles: viewer / on-call / admin, enforced on API and SSE
5. Login form, httpOnly + SameSite cookies
6. Audit log (every mutation, with identity)
7. Attack lab: forge, escalate, replay, brute-force — all rejected, all logged

## I8 — gRPC (measure, don't believe)

1. Protobuf: contracts as code (proto/ + buf lint)
2. gRPC server + client from the same .proto
3. Streaming
4. Breaking-change detection in CI
5. Benchmark REST vs gRPC with k6 — p50/p99/payload honestly measured
6. Adopt gRPC internally, keep REST externally

## I9 — Load, limits, ops page

1. k6: ramp traffic until death
2. Find exactly one bottleneck, fix it, re-measure
3. Rate limiting + timeouts at the gateway
4. Live ops page (ingest rate, queue depth, p99)
5. Publish the load report; fresh-clone checkpoint #2

## I10 — AWS as code

1. Terraform basics: providers, resources, plan/apply, state
2. VPC module from scratch
3. RDS, ElastiCache, SQS as modules
4. Least-privilege IAM (walk one role back from over-privileged)
5. Remote state + locking
6. Plan gate in CI + budget alarms
7. Drift lab; destroy-everything-and-rebuild-from-zero lab

## I11 — Kubernetes deployment

1. Docker images for our services (Dockerfile from first principles)
2. K8s nouns: pods, deployments, services, ingresses
3. EKS + spot nodes; ECR; images on GHCR
4. Helm chart per service (the user install path)
5. CI pipeline: build → push → plan gate → deploy on merge
6. Canary + error-rate gate + automatic rollback
7. Lab: merge a broken build on purpose, watch it roll back

## I12 — SLOs & runbooks

1. What an SLO is; error budgets
2. Recording rules + multi-window burn-rate alerts
3. Error-budget dashboard
4. Three real runbooks
5. Violate your own SLO; follow your own runbook at 11 PM; fix the runbook
6. Friend test (README-only deploy); real postmortem in the repo

## I13 — Can an LLM read my cluster?

1. What an LLM API call actually is (raw, no frameworks, Python)
2. Ollama: local-first, zero keys, zero spend
3. Hallucination lab: ask with no evidence
4. Query Prometheus/Loki, paste results into the prompt, ask again — feel grounding
5. The provider adapter (openai/anthropic/bedrock/ollama behind one interface)
6. Incident detail page: timeline, RCA draft, evidence links
7. Compare 2–3 models on one incident

## I14 — Give it hands (tools)

1. Tool calling: what the loop actually is (LLM asks → we run → we answer)
2. Typed tools: query_promql, search_logs, get_recent_deploys, get_service_topology
3. Investigator service consuming incidents from the queue
4. Store & render the full tool-call trace (the debugging surface)
5. Config-gated tools (users disable what they don't want)
6. Rabbit-hole lab: watch five investigations, tune prompts

## I15 — MCP + guardrails

1. What MCP is (tools as a standard protocol)
2. Expose our toolset as an MCP server
3. Policy layer: per-tool allowlists, rate caps, read-only by default
4. Audit with caller identity
5. Lab: connect your own MCP client, try to talk it into something unauthorized, watch policy reject it — in the audit log

## I16 — Remediation with human approval

1. Remediation tools: restart_pod, rollback_deployment (dry-run by default)
2. The approval gate: propose → approve/reject card → RBAC → scoped ServiceAccount → verify → auto-resolve
3. Approvals restricted to on-call/admin; UI hides what API denies (defense in depth)
4. Timeline records the approver's identity
5. Labs: approve a wrong fix in a sandbox, study blast radius; approve as viewer, get denied twice

## I17 — Evals (prove it with numbers)

1. Codify 15–20 reproducible chaos scenarios (the fixture format)
2. Eval harness: score cause/evidence/fix correctness + tokens/cost/time
3. Results table page (one table, zero chart libraries)
4. Matrix runs: models × prompts × tools → leaderboard
5. copilot-eval as a standalone tool; fixture docs = first good-first-issues
6. Fresh-clone checkpoint #3

## I18 — The full loop, unattended

1. Chaos-as-CI: push → deploy → inject → detect → diagnose → auto-approve (demo only) → remediate → verify → report on the PR
2. Run it 10×; measure consistency; chase the flake
3. Publish the loop's reliability stats

## I19 — Ship

1. v0.1.0: CHANGELOG + semver discipline
2. Docs site + quickstart + BYO guides (your cluster / LLM / IdP / AWS)
3. 2-minute demo video; architecture diagrams
4. Stranger test: someone installs BYO mode unassisted; fix every stumble
5. Public launch — the one screenshot: Alert → RCA → Approve → Resolved

---

## Standing rules (see LEARNING.md)

- One rung at a time; every line of code explained; every command explained before it runs
- Each rung ends with a live demo you can re-run yourself
- Commit per rung (plain sentence), push at session end
- Side quests capped at one day; every detour gets a one-paragraph write-up in chat
- The plan's increments (I1–I19) and their order come from the master plan; rungs inside each are refined as we arrive
