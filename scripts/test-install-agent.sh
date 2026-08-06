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
  'docs/agent-onboarding.md'
)
for pattern in "${required_patterns[@]}"; do
  grep -F -- "$pattern" "$script" >/dev/null || {
    printf 'install-agent policy missing: %s\n' "$pattern" >&2
    exit 1
  }
done

if grep -F 'sudo cat /etc/lodge-agent/token' "$script" >/dev/null; then
  printf 'install-agent must not tell operators to print the bearer token\n' >&2
  exit 1
fi

printf 'install-agent policy tests: PASS\n'
