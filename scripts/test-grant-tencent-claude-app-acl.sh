#!/usr/bin/env bash
set -euo pipefail

script="deploy/grant-tencent-claude-app-acl.sh"

for pattern in \
  'readonly PROJECT_DIR="/home/dev/claude-app"' \
  'readonly LEGACY_USER="dev"' \
  'readonly ADMIN_USER="lodge-admin"' \
  'readonly BACKUP_ROOT="/root/lodge-acl-backups"' \
  'getfacl --absolute-names --physical --recursive "$PROJECT_DIR" >"$backup_path"' \
  'setfacl --physical --recursive --modify "u:${ADMIN_USER}:rwX,m::rwX" "$PROJECT_DIR"'
do
  grep -Fx "$pattern" "$script" >/dev/null || {
    printf 'project ACL handover policy missing: %s\n' "$pattern" >&2
    exit 1
  }
done

grep -F 'find "$PROJECT_DIR" -type d -print0' "$script" >/dev/null || {
  printf 'project ACL handover must apply defaults only to directories\n' >&2
  exit 1
}

grep -F 'setfacl --restore=<该文件>' "$script" >/dev/null || {
  printf 'project ACL handover must disclose its precise restore form\n' >&2
  exit 1
}

if rg -n 'usermod|docker|sudoers|/home/dev"' "$script" >/dev/null; then
  printf 'project ACL handover must not broaden identity or host authority\n' >&2
  exit 1
fi

printf 'project ACL handover policy tests: PASS\n'
