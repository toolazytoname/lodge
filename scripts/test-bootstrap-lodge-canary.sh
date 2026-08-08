#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

script="deploy/bootstrap-lodge-canary.sh"
bash -n "$script"
grep -Fx 'readonly PROJECT_DIR="/srv/lodge-canary"' "$script" >/dev/null
grep -Fx 'readonly LOOPBACK_PORT="18080"' "$script" >/dev/null
grep -Fx 'readonly TEMP_SUDOERS_FILE="/etc/sudoers.d/lodge-canary-bootstrap"' "$script" >/dev/null
grep -Fx 'readonly TEMP_SCRIPT_FILE="/usr/local/sbin/lodge-bootstrap-canary-once.sh"' "$script" >/dev/null
grep -Fx 'readonly TEMP_SUDOERS_CONTENT="lodge-admin ALL=(root) NOPASSWD: /usr/bin/bash /usr/local/sbin/lodge-bootstrap-canary-once.sh"' "$script" >/dev/null
grep -F 'a failed bootstrap cannot leave deployment authority behind' "$script" >/dev/null
grep -F 'rm -f -- "$TEMP_SUDOERS_FILE"' "$script" >/dev/null
grep -F 'rm -f -- "$TEMP_SCRIPT_FILE"' "$script" >/dev/null
grep -F '127.0.0.1:${LOOPBACK_PORT}:80' "$script" >/dev/null
grep -F 'mem_limit: 64m' "$script" >/dev/null
grep -F 'pids_limit: 32' "$script" >/dev/null
grep -F 'restart: "no"' "$script" >/dev/null
grep -F '[ ! -e "$POLICY_FILE" ] && [ ! -L "$POLICY_FILE" ]' "$script" >/dev/null
grep -F 'install -o root -g root -m 0600 "$tmp_dir/deployments.json" "$POLICY_FILE"' "$script" >/dev/null
grep -F 'nginx@sha256:814a8e88df978ade80e584cc5b333144b9372a8e3c98872d07137dbf3b44d0e4' "$script" >/dev/null
grep -F 'nginx@sha256:4ff102c5d78d254a6f0da062b3cf39eaf07f01eec0927fd21e219d0af8bc0591' "$script" >/dev/null
printf 'bootstrap canary policy tests: PASS\n'
