#!/usr/bin/env bash
# Configure or verify a Lodge endpoint exposed by Tailscale Serve only.
#
# Usage:
#   tailnet-management.sh check hub [local-port] [serve-port]
#   tailnet-management.sh apply hub [local-port] [serve-port]
#   tailnet-management.sh check agent [local-port] [serve-port]
#   tailnet-management.sh apply agent [local-port] [serve-port]

set -euo pipefail

fail() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

info() {
  printf '%s\n' "$*"
}

usage() {
  sed -n '2,9p' "$0" >&2
  exit 2
}

[ "$#" -ge 2 ] && [ "$#" -le 4 ] || usage
action="$1"
role="$2"

case "$action" in
  check | apply) ;;
  *) usage ;;
esac

case "$role" in
  hub)
    default_local_port=9102
    default_serve_port=10000
    serve_mode=web
    serve_protocol=HTTPS
    serve_flag=--https
    funnel_flag=--https
    ;;
  agent)
    default_local_port=9101
    default_serve_port=8443
    serve_mode=tcp
    serve_protocol=TCP
    serve_flag=--tcp
    funnel_flag=--tcp
    ;;
  *) usage ;;
esac

local_port="${3:-$default_local_port}"
serve_port="${4:-$default_serve_port}"
for port in "$local_port" "$serve_port"; do
  case "$port" in
    '' | *[!0-9]*) fail "ports must be decimal integers" ;;
  esac
  [ "$port" -ge 1 ] && [ "$port" -le 65535 ] || fail "port outside 1-65535: $port"
done

for command_name in tailscale ss curl awk python3; do
  command -v "$command_name" >/dev/null 2>&1 || fail "missing required command: $command_name"
done

tailscale_version="$(tailscale version | awk 'NR == 1 { print $1 }')"
version_core="${tailscale_version#v}"
version_major="${version_core%%.*}"
version_rest="${version_core#*.}"
version_minor="${version_rest%%.*}"
case "$version_major:$version_minor" in
  *[!0-9:]* | :* | *:) fail "cannot parse Tailscale version: $tailscale_version" ;;
esac
if [ "$version_major" -lt 1 ] || { [ "$version_major" -eq 1 ] && [ "$version_minor" -lt 52 ]; }; then
  fail "Tailscale 1.52 or newer is required; found $tailscale_version"
fi

tailscale ip -4 >/dev/null 2>&1 || fail "Tailscale is not connected"

listeners="$(ss -H -lnt | awk -v suffix=":$local_port" 'length($4) >= length(suffix) && substr($4, length($4) - length(suffix) + 1) == suffix { print $4 }')"
[ -n "$listeners" ] || fail "nothing is listening on local port $local_port"
while IFS= read -r address; do
  case "$address" in
    "127.0.0.1:$local_port" | "[::1]:$local_port") ;;
    *) fail "management service is not loopback-only: $address" ;;
  esac
done <<EOF
$listeners
EOF

case "$role" in
  hub)
    health_path=/api/session
    expected_status=200
    ;;
  agent)
    health_path=/v1/ping
    expected_status=401
    ;;
esac
health_status="$(curl --max-time 3 --silent --show-error --output /dev/null --write-out '%{http_code}' "http://127.0.0.1:$local_port$health_path")" \
  || fail "local $role endpoint on port $local_port is not responding"
[ "$health_status" = "$expected_status" ] \
  || fail "local $role health endpoint returned HTTP $health_status; expected $expected_status"

funnel_status() {
  tailscale funnel status --json
}

serve_status() {
  tailscale serve status --json
}

funnel_enabled_on_port() {
  funnel_status | python3 -c '
import json, sys
port = sys.argv[1]
status = json.load(sys.stdin)
enabled = any(key.endswith(":" + port) and value is True for key, value in status.get("AllowFunnel", {}).items())
raise SystemExit(0 if enabled else 1)
' "$serve_port"
}

verify_private_serve() {
  if funnel_enabled_on_port; then
    fail "Tailscale Funnel is still public on management port $serve_port"
  fi
  if ! serve_status | python3 -c '
import json, sys
serve_port, local_port, mode, protocol = sys.argv[1:]
status = json.load(sys.stdin)
expected = "http://127.0.0.1:" + local_port
tcp = status.get("TCP", {}).get(serve_port, {})
web = status.get("Web", {})
proxy_matches = any(
    key.endswith(":" + serve_port)
    and any(handler.get("Proxy") == expected for handler in value.get("Handlers", {}).values())
    for key, value in web.items()
)
valid = (
    tcp.get(protocol) is True and proxy_matches
    if mode == "web"
    else tcp.get("TCPForward") == "127.0.0.1:" + local_port
)
raise SystemExit(0 if valid else 1)
' "$serve_port" "$local_port" "$serve_mode" "$serve_protocol"; then
    fail "Tailscale Serve $serve_protocol $serve_port does not proxy to 127.0.0.1:$local_port"
  fi
  info "PASS: $role is loopback-bound and available through tailnet-only $serve_protocol Serve on $serve_port"
}

conflicting_serve_flag() {
  serve_status | python3 -c '
import json, sys
serve_port, local_port, mode = sys.argv[1:]
status = json.load(sys.stdin)
tcp = status.get("TCP", {}).get(serve_port, {})
web = status.get("Web", {})
http_target = "http://127.0.0.1:" + local_port
tcp_target = "127.0.0.1:" + local_port
old_http_targets_agent = tcp.get("HTTP") is True and any(
    key.endswith(":" + serve_port)
    and any(handler.get("Proxy") == http_target for handler in value.get("Handlers", {}).values())
    for key, value in web.items()
)
if mode == "tcp" and old_http_targets_agent:
    print("--http")
elif mode == "web" and tcp.get("TCPForward") == tcp_target:
    print("--tcp")
else:
    raise SystemExit(1)
' "$serve_port" "$local_port" "$serve_mode"
}

if [ "$action" = check ]; then
  verify_private_serve
  exit 0
fi

[ "$(id -u)" -eq 0 ] || fail "apply must run as root"
backup_root="${LODGE_TAILSCALE_BACKUP_DIR:-/var/lib/lodge/tailscale-backups}"
backup_run="$backup_root/$(date -u +%Y%m%dT%H%M%SZ)-$$"
install -d -m 0700 "$backup_run"
umask 077
funnel_status >"$backup_run/funnel-status.json"
serve_status >"$backup_run/serve-status.json"
tailscale version >"$backup_run/tailscale-version.txt"
info "Saved pre-change status to $backup_run"

if funnel_enabled_on_port; then
  tailscale funnel "$funnel_flag=$serve_port" off
  info "Disabled public Funnel on management port $serve_port"
fi
if old_serve_flag="$(conflicting_serve_flag)"; then
  tailscale serve "$old_serve_flag=$serve_port" off
  info "Disabled prior Lodge Serve mode on management port $serve_port"
fi
case "$serve_mode" in
  web) serve_target="http://127.0.0.1:$local_port" ;;
  tcp) serve_target="tcp://127.0.0.1:$local_port" ;;
esac
tailscale serve --bg --yes "$serve_flag=$serve_port" "$serve_target"
verify_private_serve

info "Recovery: keep SSH open; if browser access fails, inspect grants and rerun this command."
info "Emergency local access: ssh -L $local_port:127.0.0.1:$local_port <host>"
