#!/usr/bin/env bash
# Generate the TypeScript HTTP contract and compile the framework-free browser app.
set -euo pipefail
cd "$(dirname "$0")/.."

go run ./cmd/lodge-web-types

build_dir="$(mktemp -d "${TMPDIR:-/tmp}/lodge-web-build.XXXXXX")"
trap 'rm -rf -- "$build_dir"' EXIT
./node_modules/.bin/tsc -p frontend/tsconfig.json --outDir "$build_dir"
install -m 0644 "$build_dir/app.js" internal/hub/web/app.js
