#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

script=deploy/install-agent.sh
bash -n "$script"

required_patterns=(
  'visudo -cf "$TMP_SUDOERS"'
  'visudo -c >/dev/null'
  'install -m 0440 -o root -g root "$SUDOERS_BACKUP" "$SUDOERS_FILE"'
  '完整 sudoers 策略校验失败，已恢复安装前策略'
  'NO_NEW_PRIVS="$(awk'
  '服务进程采集通过：services='
  'docs/agent-onboarding.md'
)
for pattern in "${required_patterns[@]}"; do
  grep -F -- "$pattern" "$script" >/dev/null || {
    printf 'install-agent policy missing: %s\n' "$pattern" >&2
    exit 1
  }
done

for forbidden_directive in \
  NoNewPrivileges CapabilityBoundingSet PrivateDevices ProtectClock \
  ProtectKernelLogs ProtectKernelModules ProtectKernelTunables \
  RestrictAddressFamilies RestrictSUIDSGID; do
  if grep -Eq "^[[:space:]]*${forbidden_directive}=" deploy/lodge-agent.service; then
    printf 'lodge-agent unit breaks the sudo boundary with %s\n' "$forbidden_directive" >&2
    exit 1
  fi
done

if grep -F 'sudo cat /etc/lodge-agent/token' "$script" >/dev/null; then
  printf 'install-agent must not tell operators to print the bearer token\n' >&2
  exit 1
fi

printf 'install-agent policy tests: PASS\n'
