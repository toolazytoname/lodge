#!/usr/bin/env bash
# Transactionally install or upgrade lodge-hub on one systemd host.
#
# Usage:
#   sudo ./install-hub.sh apply /path/to/lodge-hub [expected-sha256]
#
# The script stops only the Hub, creates an owner-only rollback bundle, migrates
# a legacy plaintext password inside the Hub binary, installs the binary/unit,
# verifies HTTP and SQLite health, and restores the previous state on failure.

set -Eeuo pipefail

action="${1:-}"
binary_source="${2:-}"
expected_sha256="${3:-}"

install_binary=/usr/local/bin/lodge-hub
candidate_binary="/usr/local/bin/.lodge-hub-candidate-$$"
unit_source="$(cd "$(dirname "$0")" && pwd)/lodge-hub.service"
unit_destination=/etc/systemd/system/lodge-hub.service
config_directory=/etc/lodge-hub
config_path="$config_directory/config.json"
legacy_state_path="$config_directory/state.json"
session_secret_path="$config_directory/session-secret"
database_directory=/var/lib/lodge-hub
database_path="$database_directory/lodge.db"
rollback_root=/var/lib/lodge-deploy-backups

service_was_active=0
service_was_stopped=0
rollback_ready=0
deployment_complete=0
rollback_directory=""

fail() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

info() {
  printf '%s\n' "$*"
}

restore_optional_file() {
  local marker="$1"
  local backup="$2"
  local destination="$3"
  if [ -f "$marker" ]; then
    cp -a -- "$backup" "$destination"
  else
    if [ -e "$destination" ]; then
      mv -- "$destination" "$rollback_directory/failed-$(basename "$destination")"
    fi
  fi
}

rollback() {
  local original_status="$1"
  trap - EXIT
  set +e
  if [ "$deployment_complete" -eq 0 ] && [ "$service_was_stopped" -eq 1 ]; then
    info "Deployment failed; restoring previous Hub state from $rollback_directory"
    systemctl stop lodge-hub >/dev/null 2>&1
    if [ "$rollback_ready" -eq 1 ]; then
      install -o root -g root -m 0755 "$rollback_directory/lodge-hub" "$install_binary"
      install -o root -g root -m 0644 "$rollback_directory/lodge-hub.service" "$unit_destination"
      cp -a -- "$rollback_directory/config.json" "$config_path"
      restore_optional_file "$rollback_directory/had-state" "$rollback_directory/state.json" "$legacy_state_path"
      restore_optional_file "$rollback_directory/had-session-secret" "$rollback_directory/session-secret" "$session_secret_path"
      for database_file in "$database_path" "$database_path-wal" "$database_path-shm"; do
        if [ -e "$database_file" ]; then
          mv -- "$database_file" "$rollback_directory/failed-$(basename "$database_file")"
        fi
      done
      if [ -f "$rollback_directory/had-database" ]; then
        install -o lodge -g lodge -m 0600 "$rollback_directory/lodge.db" "$database_path"
      fi
      systemctl daemon-reload
    fi
    if [ "$service_was_active" -eq 1 ]; then
      systemctl start lodge-hub
      systemctl is-active --quiet lodge-hub || info "WARNING: rollback service restart failed"
    fi
  fi
  if [ -e "$candidate_binary" ]; then
    rm -f -- "$candidate_binary"
  fi
  exit "$original_status"
}

trap 'rollback $?' EXIT

[ "$action" = apply ] || fail "usage: install-hub.sh apply /path/to/lodge-hub [expected-sha256]"
[ "$#" -ge 2 ] && [ "$#" -le 3 ] || fail "usage: install-hub.sh apply /path/to/lodge-hub [expected-sha256]"
[ "$(id -u)" -eq 0 ] || fail "must run as root"
[ -f "$binary_source" ] || fail "Hub binary not found: $binary_source"
[ -f "$unit_source" ] || fail "unit file not found: $unit_source"
[ -f "$config_path" ] || fail "Hub config not found: $config_path"
for required_command in systemctl systemd-analyze curl python3 runuser sha256sum mktemp; do
  command -v "$required_command" >/dev/null 2>&1 || fail "missing command: $required_command"
done
id lodge >/dev/null 2>&1 || fail "service account lodge does not exist"
for privileged_group in docker wheel sudo adm; do
  if id -nG lodge | tr ' ' '\n' | grep -qx "$privileged_group"; then
    fail "service account lodge belongs to privileged group $privileged_group"
  fi
done

install -o root -g root -m 0755 "$binary_source" "$candidate_binary"
actual_sha256="$(sha256sum "$candidate_binary" | awk '{print $1}')"
if [ -n "$expected_sha256" ] && [ "$actual_sha256" != "$expected_sha256" ]; then
  fail "candidate SHA-256 mismatch: got $actual_sha256"
fi
"$candidate_binary" --version >/dev/null
systemd-analyze verify "$unit_source" >/dev/null
info "Preflight passed for candidate SHA-256 $actual_sha256"

install -d -o root -g root -m 0700 "$rollback_root"
rollback_directory="$(mktemp -d "$rollback_root/hub-$(date -u +%Y%m%dT%H%M%SZ)-XXXXXX")"
chmod 0700 "$rollback_directory"

if systemctl is-active --quiet lodge-hub; then
  service_was_active=1
fi
systemctl stop lodge-hub
service_was_stopped=1

cp -a -- "$install_binary" "$rollback_directory/lodge-hub"
cp -a -- "$unit_destination" "$rollback_directory/lodge-hub.service"
cp -a -- "$config_path" "$rollback_directory/config.json"
chmod 0600 "$rollback_directory/config.json"
if [ -f "$legacy_state_path" ]; then
  : >"$rollback_directory/had-state"
  cp -a -- "$legacy_state_path" "$rollback_directory/state.json"
  chmod 0600 "$rollback_directory/state.json"
fi
if [ -f "$session_secret_path" ]; then
  : >"$rollback_directory/had-session-secret"
  cp -a -- "$session_secret_path" "$rollback_directory/session-secret"
  chmod 0600 "$rollback_directory/session-secret"
fi
if [ -f "$database_path" ]; then
  : >"$rollback_directory/had-database"
  "$candidate_binary" --database "$database_path" --backup "$rollback_directory/lodge.db" >/dev/null
fi
{
  printf 'candidate_sha256=%s\n' "$actual_sha256"
  printf 'previous_binary_sha256=%s\n' "$(sha256sum "$rollback_directory/lodge-hub" | awk '{print $1}')"
  printf 'created_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
} >"$rollback_directory/deployment.meta"
chmod 0600 "$rollback_directory/deployment.meta"
rollback_ready=1
info "Rollback bundle created at $rollback_directory"

chmod 0600 "$config_path"
runuser -u lodge -- "$candidate_binary" --config "$config_path" --migrate-config-password >/dev/null
runuser -u lodge -- "$candidate_binary" --config "$config_path" --migrate-config-password >/dev/null
[ "$(stat -c %a "$config_path")" = 600 ] || fail "config mode is not 0600 after migration"

install -o root -g root -m 0755 "$candidate_binary" "$install_binary"
install -o root -g root -m 0644 "$unit_source" "$unit_destination"
systemctl daemon-reload
systemctl enable lodge-hub >/dev/null
systemctl restart lodge-hub

healthy=0
for _attempt in $(seq 1 20); do
  if systemctl is-active --quiet lodge-hub && curl --fail --silent --max-time 2 http://127.0.0.1:9102/api/session >/dev/null; then
    healthy=1
    break
  fi
  sleep 1
done
[ "$healthy" -eq 1 ] || fail "new Hub did not become healthy within 20 seconds"
systemctl show lodge-hub -p ExecStart --value | grep -F -- '--database /var/lib/lodge-hub/lodge.db' >/dev/null || fail "running unit lacks SQLite arguments"

for protected_file in "$config_path" "$session_secret_path" "$database_path" "$database_path-wal" "$database_path-shm"; do
  if [ -e "$protected_file" ]; then
    [ "$(stat -c %a "$protected_file")" = 600 ] || fail "$protected_file is not mode 0600"
    [ "$(stat -c %U:%G "$protected_file")" = lodge:lodge ] || fail "$protected_file is not owned by lodge:lodge"
  fi
done

runuser -u lodge -- python3 - "$database_path" <<'PY'
import sqlite3
import sys

connection = sqlite3.connect(f"file:{sys.argv[1]}?mode=ro", uri=True)
integrity = connection.execute("PRAGMA integrity_check").fetchone()[0]
version = connection.execute("PRAGMA user_version").fetchone()[0]
hosts = connection.execute("SELECT count(*) FROM hosts WHERE configured = 1").fetchone()[0]
observations = connection.execute("SELECT count(*) FROM observations").fetchone()[0]
sensitive = connection.execute(
    "SELECT count(*) FROM pragma_table_info('hosts') "
    "WHERE lower(name) IN ('token', 'agent_token', 'password')"
).fetchone()[0]
if integrity != "ok" or version < 1 or sensitive != 0:
    raise SystemExit("SQLite verification failed")
print(f"SQLite verified: schema={version} hosts={hosts} observations={observations}")
PY

install -d -o lodge -g lodge -m 0700 "$database_directory/backups"
post_backup="$database_directory/backups/post-deploy-$(date -u +%Y%m%dT%H%M%SZ)-$$.db"
runuser -u lodge -- "$install_binary" --database "$database_path" --backup "$post_backup" >/dev/null
[ "$(stat -c %a "$post_backup")" = 600 ] || fail "post-deploy backup is not mode 0600"

deployment_complete=1
rm -f -- "$candidate_binary"
info "Hub deployment verified"
info "Rollback bundle: $rollback_directory"
info "Post-deploy SQLite backup: $post_backup"

