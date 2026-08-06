#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

script=deploy/install-hub.sh
bash -n "$script"

required_patterns=(
  'trap '\''rollback $?'\'' EXIT'
  'chmod 0600 "$config_path"'
  '--migrate-config-password'
  '--backup "$rollback_directory/lodge.db"'
  'systemd-analyze verify'
  'http://127.0.0.1:9102/api/session'
  'PRAGMA integrity_check'
  'Post-deploy SQLite backup'
)
for pattern in "${required_patterns[@]}"; do
  grep -F -- "$pattern" "$script" >/dev/null || {
    printf 'install-hub policy missing: %s\n' "$pattern" >&2
    exit 1
  }
done

if grep -Eq 'rm[[:space:]]+-[^[:space:]]*r' "$script"; then
  printf 'install-hub must not recursively delete paths\n' >&2
  exit 1
fi

printf 'install-hub policy tests: PASS\n'
