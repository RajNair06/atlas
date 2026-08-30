# atlas

An AI SRE platform that detects, deduplicates, triages, and root-causes incidents — and remediates with human approval.

> **Status:** early development. The repo is being built in the open, one increment at a time. Right now: a deliberately naive sample stack that demonstrates cascade failure.

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

### Feel the cascade

```sh
# make payment slow: every request now waits ~3s
PAYMENT_DELAY=3s make demo

# kill payment and see what the user gets
pkill -f bin/payment
curl http://localhost:8080/buy
```

## Development

```sh
make dev     # run the sample stack locally
make test    # go test ./...
make lint    # golangci-lint (falls back to go vet)
make demo    # build + run the sample stack
make clean   # remove built binaries
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
