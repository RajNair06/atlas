#!/usr/bin/env bash
# test-all.sh — the full verification gate for atlas.
#
# Runs everything CI runs, plus a per-package coverage summary:
#   1. go build ./...            — compile check (including tests)
#   2. go vet ./...              — static analysis
#   3. go test -race -count=1    — full suite, race detector, no cached results
#   4. go test -cover            — coverage summary per package
#
# Usage: ./scripts/test-all.sh
# Exit code is non-zero if any step fails (set -e), so it is CI-safe.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_DIR"

echo "==> go build ./..."
go build ./...

echo ""
echo "==> go vet ./..."
go vet ./...

echo ""
echo "==> go test -race -count=1 ./..."
go test -race -count=1 ./...

echo ""
echo "==> coverage summary (go test -cover ./...)"
go test -cover -count=1 ./...

echo ""
echo "all checks passed"
