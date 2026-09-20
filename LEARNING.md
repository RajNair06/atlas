# LEARNING.md

**Temporary file — delete when the project is complete.**

## How we work

1. **Small steps.** One concept at a time. We never move forward until the current step is genuinely understood.
2. **First principles.** Teaching assumes zero prior knowledge of Go backends, backend concepts, DevOps, or AI. Every concept is built up from its foundation before it is used.
3. **Every line explained.** Every line of code written gets explained: what it does, why it exists, and what every variable and identifier means.
4. **Every command explained.** What it does and why we run it — before running it.
5. **No rushing.** Questions stop everything. "I don't get it" is the most useful sentence in this project.
6. **Hands-on proof.** Every concept gets demonstrated live in the demo shop, then verified with a command you can see and re-run.

## How we plan and build (the two-mode loop)

Each roadmap day is split into **two half-day chunks** (Part 1 / Part 2). Each chunk runs one full **plan → build → test → iterate** cycle:

1. **Plan mode (read-only).** The chunk is scoped precisely: files to touch, concepts to teach, traps to dodge (e.g., import cycles), open design questions with a recommendation for each. Nothing is written until the plan is approved.
2. **Build mode.** Implement in small verified steps:
   - **Concept first** — name the hard part *before* the code that solves it (why an interface, why two-phase wiring, why backoff).
   - **Code second** — write it, explain every new construct as it lands.
   - **Unit tests third** — fakes and interfaces make behavior deterministic (fake replayers, fake analyzers; timing assertions prove sleeps actually slept).
   - **Live demo fourth** — prove it end-to-end against the real demo shop and the real LLM. Tests prove correctness; demos prove reality. Both are required.
3. **Iterate on what reality says.** When a demo exposes wrong behavior (e.g., Gemini treating "connection refused + no fallback" as give_up), the fix goes in the same chunk, and the reasoning is written down. **Prompt tuning is engineering**, not magic: observe → diagnose → sharpen the prompt → re-observe.
4. **Close the chunk.** Tests green + vet clean + demo captured → ROADMAP updated → one concise commit → push.

## Roadmap formation rules

- The ROADMAP holds **days**, each day holds a demoable outcome; chunks are refined only when we arrive at them (rough sketches below the horizon, precise steps at the frontier).
- Every day ends with something a stranger could run: a command, a script, or a visible behavior change.
- Scope guard: if a chunk grows past half a day, split it and re-plan rather than pushing through.
- Detours are allowed (capped) but get a one-paragraph write-up of what they taught.

## Progress

See `ROADMAP.md` for the day-by-day plan of the current project.

**Current project:** Self-Healing API Gateway with LLM-powered error resolution (2-week plan, pivoted September 2026).

- Days 1–4 — complete: reverse proxy, demo-shop integration, error interception, Gemini analysis
- Day 5 — complete: healing executor, synchronous auto-retry with exponential backoff, fallback chain, per-upstream circuit breaker, latency budget
- Day 6 — complete: human-in-the-loop approval gate (require_approval), approval store with history, SSE healing console at /ui, demo-approval.sh
- Day 7 — next: end-to-end tests for each action type, edge cases (Gemini timeout, breaker trip, human rejection), logging cleanup

**Earlier foundation (kept and reused):** I1 naive trio + cascade labs (14 rungs), I2 observability rungs 1–3 (structured logging, correlation pain, request IDs).
