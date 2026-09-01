# atlas

An AI SRE platform that detects, deduplicates, triages, and root-causes incidents — and remediates with human approval.

> **Status:** early development. The repo is being built in the open, one increment at a time. Right now: a naive sample stack that demonstrates cascade failure — now instrumented with OpenTelemetry so every request is traceable end to end.

## Two modes

- **Demo** — bundled sample e-commerce stack plus chaos fixtures. Shows the whole loop with zero setup.
- **BYO** — bring your own Prometheus, Loki, Tempo, Kubernetes cluster, LLM, and identity provider via one config file. (Arrives in later increments.)

## Quickstart

```sh
git clone https://github.com/RajNair06/atlas.git
cd atlas
cp .env.example .env
make demo
```

Then in another terminal:

```sh
curl http://localhost:8080/buy
```

Watch a request flow `storefront → checkout → payment` and come back.

### See inside the black box

Start the observability stack (OpenTelemetry Collector + Jaeger), then run the shop again:

```sh
make obs-up     # collector on :4317, Jaeger UI on :16686
make demo
```

```sh
curl http://localhost:8080/buy
```

Open **http://localhost:16686**, pick service `storefront`, and find the trace. You'll see the full span tree — `GET /buy` → `POST /checkout` → `POST /pay` — with a duration on every hop.

Make payment slow and watch the cause surface in one query:

```sh
PAYMENT_DELAY=3s make demo
curl http://localhost:8080/buy
```

In Jaeger, filter by `minDuration = 2s`. The deepest span (`POST /pay`) is visibly the culprit. Every log line carries the same `trace_id`, so logs and traces join on it.

RED metrics (rate / errors / duration) are exported by the collector at **http://localhost:8889/metrics**.

### Feel the cascade

```sh
# make payment slow: every request now waits ~3s
PAYMENT_DELAY=3s make demo

# kill payment and see what the user gets
pkill -x payment
curl http://localhost:8080/buy
```

## Development

```sh
make dev       # run the sample stack locally
make test      # go test ./...
make demo      # build + run the sample stack
make obs-up    # start the observability stack (collector + Jaeger)
make obs-down  # stop it
make clean     # remove built binaries
```

## Repository layout

```text
services/            platform services (added in later increments)
agent/               AI investigation agent (Python, later increments)
infra/               Terraform modules (later increments)
deploy/              Helm charts and manifests (later increments)
chaos/               fault-injection fixtures
web/                 UI (Go templates + htmx, later increments)
examples/demo-shop/  sample e-commerce stack: storefront, checkout, payment
```

The sample stack under `examples/` is a fixture. It is deliberately separate from the product code so it can never be mistaken for it.
