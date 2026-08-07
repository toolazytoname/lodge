#!/usr/bin/env bash
# Prove committed generated types and browser JavaScript match their sources.
set -euo pipefail
cd "$(dirname "$0")/.."

go run ./cmd/lodge-web-types --check

build_dir="$(mktemp -d "${TMPDIR:-/tmp}/lodge-web-check.XXXXXX")"
trap 'rm -rf -- "$build_dir"' EXIT
./node_modules/.bin/tsc -p frontend/tsconfig.json --outDir "$build_dir"
cmp "$build_dir/app.js" internal/hub/web/app.js
cmp frontend/src/app.css internal/hub/web/app.css
node --check internal/hub/web/app.js
