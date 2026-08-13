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

step "unit tests with race detector"
go test -race ./...

step "deployment scripts"
bash -n deploy/*.sh scripts/*.sh
bash scripts/test-tailnet-management.sh
bash scripts/test-install-hub.sh
bash scripts/test-install-agent.sh
bash scripts/test-bootstrap-lodge-canary.sh
bash scripts/test-grant-tencent-claude-app-acl.sh
bash scripts/test-cliproxyapi-updater.sh

step "generated TypeScript Web contract and build"
npm run check:web

step "responsive Web end-to-end and visual regression"
npm run test:web:e2e

step "quality scorecard schema"
python3 -m json.tool quality/scorecard.json >/dev/null

step "whitespace"
git diff --check

printf '\n[quality] PASS — all gates green\n'
