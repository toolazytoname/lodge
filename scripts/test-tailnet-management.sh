#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

test_dir="$(mktemp -d)"
trap 'find "$test_dir" -depth -delete' EXIT
mock_bin="$test_dir/bin"
mkdir -p "$mock_bin"
hub_funnel_state="$test_dir/hub-funnel-enabled"
agent_funnel_state="$test_dir/agent-funnel-enabled"
legacy_agent_http_state="$test_dir/legacy-agent-http"
command_log="$test_dir/commands.log"

cat >"$mock_bin/tailscale" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$MOCK_COMMAND_LOG"
case "${1:-} ${2:-}" in
  "version ") printf '1.98.9\n' ;;
  "ip -4") printf '100.64.0.10\n' ;;
  "funnel status")
    if [ -f "$MOCK_HUB_FUNNEL_STATE" ]; then
      allow_funnel='{"host.example.ts.net:10000":true}'
    elif [ -f "$MOCK_AGENT_FUNNEL_STATE" ]; then
      allow_funnel='{"host.example.ts.net:8443":true}'
    else
      allow_funnel='{}'
    fi
    if [ -f "$MOCK_LEGACY_AGENT_HTTP_STATE" ]; then
      printf '{"TCP":{"10000":{"HTTPS":true},"8443":{"HTTP":true}},"Web":{"host.example.ts.net:10000":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:9102"}}},"host.example.ts.net:8443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:9101"}}}},"AllowFunnel":%s}\n' "$allow_funnel"
    else
      printf '{"TCP":{"10000":{"HTTPS":true},"8443":{"TCPForward":"127.0.0.1:9101"}},"Web":{"host.example.ts.net:10000":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:9102"}}}},"AllowFunnel":%s}\n' "$allow_funnel"
    fi
    ;;
  "serve status")
    if [ -f "$MOCK_HUB_FUNNEL_STATE" ]; then
      allow_funnel='{"host.example.ts.net:10000":true}'
    elif [ -f "$MOCK_AGENT_FUNNEL_STATE" ]; then
      allow_funnel='{"host.example.ts.net:8443":true}'
    else
      allow_funnel='{}'
    fi
    if [ -f "$MOCK_LEGACY_AGENT_HTTP_STATE" ]; then
      printf '{"TCP":{"10000":{"HTTPS":true},"8443":{"HTTP":true}},"Web":{"host.example.ts.net:10000":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:9102"}}},"host.example.ts.net:8443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:9101"}}}},"AllowFunnel":%s}\n' "$allow_funnel"
    else
      printf '{"TCP":{"10000":{"HTTPS":true},"8443":{"TCPForward":"127.0.0.1:9101"}},"Web":{"host.example.ts.net:10000":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:9102"}}}},"AllowFunnel":%s}\n' "$allow_funnel"
    fi
    ;;
  "funnel --https=10000")
    [ "${3:-}" = off ] || exit 2
    find "$MOCK_HUB_FUNNEL_STATE" -depth -delete
    ;;
  "funnel --tcp=8443")
    [ "${3:-}" = off ] || exit 2
    find "$MOCK_AGENT_FUNNEL_STATE" -depth -delete
    ;;
  "serve --http=8443")
    [ "${3:-}" = off ] || exit 2
    find "$MOCK_LEGACY_AGENT_HTTP_STATE" -depth -delete
    ;;
  "serve --bg") ;;
  *) printf 'unexpected tailscale command: %s\n' "$*" >&2; exit 2 ;;
esac
EOF

cat >"$mock_bin/ss" <<'EOF'
#!/usr/bin/env bash
printf 'LISTEN 0 4096 *:22 *:*\n'
printf 'LISTEN 0 4096 127.0.0.1:9101 0.0.0.0:*\n'
printf 'LISTEN 0 4096 127.0.0.1:9102 0.0.0.0:*\n'
EOF

cat >"$mock_bin/curl" <<'EOF'
#!/usr/bin/env bash
case "${*: -1}" in
  */api/session) printf '200' ;;
  */v1/ping) printf '401' ;;
  *) exit 2 ;;
esac
EOF

cat >"$mock_bin/id" <<'EOF'
#!/usr/bin/env bash
if [ "${1:-}" = -u ]; then
  printf '0\n'
else
  /usr/bin/id "$@"
fi
EOF

chmod +x "$mock_bin"/*
export PATH="$mock_bin:$PATH"
export MOCK_HUB_FUNNEL_STATE="$hub_funnel_state"
export MOCK_AGENT_FUNNEL_STATE="$agent_funnel_state"
export MOCK_LEGACY_AGENT_HTTP_STATE="$legacy_agent_http_state"
export MOCK_COMMAND_LOG="$command_log"
export LODGE_TAILSCALE_BACKUP_DIR="$test_dir/backups"

touch "$hub_funnel_state"
if deploy/tailnet-management.sh check hub >/dev/null 2>&1; then
  printf 'check unexpectedly accepted a public Funnel\n' >&2
  exit 1
fi

deploy/tailnet-management.sh apply hub >/dev/null
deploy/tailnet-management.sh check hub >/dev/null
deploy/tailnet-management.sh check agent >/dev/null
touch "$agent_funnel_state"
touch "$legacy_agent_http_state"
deploy/tailnet-management.sh apply agent >/dev/null

grep -Fxq 'funnel --https=10000 off' "$command_log"
grep -Fxq 'funnel --tcp=8443 off' "$command_log"
grep -Fxq 'serve --http=8443 off' "$command_log"
grep -Fxq 'serve --bg --yes --https=10000 http://127.0.0.1:9102' "$command_log"
grep -Fxq 'serve --bg --yes --tcp=8443 tcp://127.0.0.1:9101' "$command_log"
test "$(find "$test_dir/backups" -type f | wc -l | tr -d ' ')" -eq 6

printf 'tailnet management tests: PASS\n'
