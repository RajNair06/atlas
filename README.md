# atlas

An AI SRE platform that detects, deduplicates, triages, and root-causes incidents — and remediates with human approval.

> **Status:** rebuilding the sample stack from first principles. The naive demo shop is being reconstructed line by line as a learning exercise — see `LEARNING.md` for the working agreement. Documentation here grows back as each piece is rebuilt.

## Repository layout

```text
services/            platform services (added in later increments)
agent/               AI investigation agent (Python, later increments)
infra/               Terraform modules (later increments)
deploy/              Helm charts and manifests (later increments)
chaos/               fault-injection fixtures
web/                 UI (Go templates + htmx, later increments)
examples/demo-shop/  sample e-commerce stack: storefront, checkout, payment (under reconstruction)
```

The sample stack under `examples/` is a fixture. It is deliberately separate from the product code so it can never be mistaken for it.
