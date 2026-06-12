#!/usr/bin/env bash
# Lodge pre-commit / pre-deploy gate.
# Runs inside atelier VM (Node 24 + Playwright). Fails fast on any error.
#
# Usage:
#   npm test               # both gates
#   npm run lint:js        # syntax check only
#   npm run test:e2e       # Playwright happy path only

set -euo pipefail
cd "$(dirname "$0")/.."

echo "[test] 1/2  inline JS syntax (acorn)"
node check-js.sh

echo "[test] 2/2  E2E happy path (playwright)"
node scripts/test-e2e.mjs

echo "[test] PASS — all gates green"
