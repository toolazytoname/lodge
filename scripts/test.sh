#!/usr/bin/env bash
# Lodge pre-commit gate.
set -euo pipefail
cd "$(dirname "$0")/.."

echo "[test] go build"
go build ./...

echo "[test] go test"
go test ./...

echo "[test] PASS — all gates green"
