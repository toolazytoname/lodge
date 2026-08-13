#!/usr/bin/env bash
# Generate the TypeScript HTTP contract and compile the framework-free browser app.
set -euo pipefail
cd "$(dirname "$0")/.."

go run ./cmd/lodge-web-types

build_dir="$(mktemp -d "${TMPDIR:-/tmp}/lodge-web-build.XXXXXX")"
trap 'rm -rf -- "$build_dir"' EXIT
./node_modules/.bin/tsc -p frontend/tsconfig.json --outDir "$build_dir"
while IFS= read -r -d '' file; do
  install -m 0644 "$file" "internal/hub/web/$(basename "$file")"
done < <(find "$build_dir" -type f -name '*.js' -print0)
install -m 0644 frontend/src/app.css internal/hub/web/app.css
