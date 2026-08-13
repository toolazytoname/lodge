#!/usr/bin/env bash
# Prove committed generated types and browser JavaScript match their sources.
set -euo pipefail
cd "$(dirname "$0")/.."

go run ./cmd/lodge-web-types --check

build_dir="$(mktemp -d "${TMPDIR:-/tmp}/lodge-web-check.XXXXXX")"
trap 'rm -rf -- "$build_dir"' EXIT
./node_modules/.bin/tsc -p frontend/tsconfig.json --outDir "$build_dir"
while IFS= read -r -d '' file; do
  cmp "$file" "internal/hub/web/$(basename "$file")"
done < <(find "$build_dir" -type f -name '*.js' -print0)
cmp frontend/src/app.css internal/hub/web/app.css
./node_modules/.bin/tsc -p frontend/tests/tsconfig.json
node --check internal/hub/web/app.js
node --check internal/hub/web/dom.js
node --check internal/hub/web/format.js
node --check scripts/web-fixture-server.mjs
