#!/usr/bin/env bash
# Lodge pre-commit / pre-deploy gate.
# Runs inside atelier VM (Node 24 + Playwright). Fails fast on any error.
#
# Usage:
#   npm test               # all gates
#   npm run lint:js        # syntax check only
#   npm run test:e2e       # Playwright happy path only

set -euo pipefail
cd "$(dirname "$0")/.."

echo "[test] 1/3  inline JS syntax (acorn)"
node check-js.sh

echo "[test] 2/3  Vercel route rules (root → dashboard, index.html → about.html)"
# Static check: prevent anyone silently dropping the / → /dashboard.html
# redirect. The local python http.server can't exercise the rule, so we
# grep the config instead.
grep -q '"source": "/"' vercel.json \
  || { echo "FAIL: vercel.json is missing the / → /dashboard.html redirect"; exit 1; }
grep -q '"/index.html"' vercel.json \
  || { echo "FAIL: vercel.json is missing the /index.html → /about.html redirect"; exit 1; }
grep -q '"destination": "/about.html"' vercel.json \
  || { echo "FAIL: vercel.json is missing /about.html destination"; exit 1; }
echo "  ✓ / → /dashboard.html"
echo "  ✓ /index.html → /about.html"

echo "[test] 3/3  E2E happy path (playwright)"
node scripts/test-e2e.mjs

echo "[test] PASS — all gates green"
