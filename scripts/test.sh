#!/usr/bin/env bash
# Lodge local/CI quality gate. Keep this deterministic and side-effect free.
set -euo pipefail
cd "$(dirname "$0")/.."

step() { printf '\n[quality] %s\n' "$1"; }

step "format"
unformatted="$(find cmd internal -type f -name '*.go' -exec gofmt -l {} +)"
if [ -n "$unformatted" ]; then
  printf 'Go files need gofmt:\n%s\n' "$unformatted" >&2
  exit 1
fi

step "vet"
go vet ./...

step "build"
go build ./...

step "unit tests"
go test ./...

step "race detector"
go test -race ./...

step "deployment scripts"
bash -n deploy/*.sh scripts/*.sh
bash scripts/test-tailnet-management.sh

step "browser JavaScript syntax"
node --check internal/hub/web/app.js

step "quality scorecard schema"
python3 -m json.tool quality/scorecard.json >/dev/null

step "whitespace"
git diff --check

printf '\n[quality] PASS — all gates green\n'
