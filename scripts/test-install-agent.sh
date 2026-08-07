#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

script=deploy/install-agent.sh
bash -n "$script"

required_patterns=(
  'visudo -cf "$TMP_SUDOERS"'
  'visudo -c >"$SUDOERS_AFTER" 2>&1'
  'install -m 0440 -o root -g root "$SUDOERS_BACKUP" "$SUDOERS_FILE"'
  'cmp -s "$SUDOERS_BASELINE_ERRORS" "$SUDOERS_AFTER_ERRORS"'
  '完整 sudoers 策略出现新增错误，已恢复安装前 Lodge 策略'
  'NO_NEW_PRIVS="$(awk'
  '服务进程采集通过：services='
  'service-context SSH authentication summary is missing or invalid'
  'service-context controlled action list is missing or invalid'
  'service-context declarative deployment list is missing or invalid'
  'install -d -o root -g lodge -m 0750 "$CONF_DIR"'
  '[ ! -L "$CONF_DIR" ]'
  "stat -c '%u' \"\$ACTION_POLICY_FILE\""
  "stat -c '%a' \"\$ACTION_POLICY_FILE\""
  "stat -c '%u' \"\$DEPLOYMENT_POLICY_FILE\""
  "stat -c '%a' \"\$DEPLOYMENT_POLICY_FILE\""
  'install -d -o root -g root -m 0700 "$DEPLOYMENT_STATE_DIR"'
  '所有写操作保持禁用（fail closed）'
  'docker system prune -f'
  'journalctl --vacuum-time=7d'
  'systemctl restart caddy'
  '--execute-action restart:systemd:caddy.service'
  '--execute-deployment deploy:gateway:latest'
  'docs/agent-onboarding.md'
)
for pattern in "${required_patterns[@]}"; do
  grep -F -- "$pattern" "$script" >/dev/null || {
    printf 'install-agent policy missing: %s\n' "$pattern" >&2
    exit 1
  }
done

if grep -Eq '^[[:space:]]*ReadWritePaths=/etc/lodge-agent' deploy/lodge-agent.service; then
  printf 'lodge-agent must not be able to replace its root-owned action policy\n' >&2
  exit 1
fi
grep -Fx 'ReadWritePaths=/var/lib/lodge-agent/deployments' deploy/lodge-agent.service >/dev/null || {
  printf 'lodge-agent unit must expose only the root-owned deployment state directory for writes\n' >&2
  exit 1
}

python3 - <<'PY'
import json

with open("deploy/agent-actions.example.json", encoding="utf-8") as stream:
    policy = json.load(stream)
assert policy["version"] == 1
assert isinstance(policy["targets"], list)
for target in policy["targets"]:
    assert set(target) == {"key", "label", "kind", "resource", "actions"}
    assert set(target["actions"]) <= {"start", "stop", "restart", "logs"}
PY

python3 - <<'PY'
import json

with open("deploy/agent-deployments.example.json", encoding="utf-8") as stream:
    policy = json.load(stream)
assert policy["version"] == 1
assert isinstance(policy["stacks"], list)
for stack in policy["stacks"]:
    assert set(stack) == {"key", "label", "projectDirectory", "composeFile", "service", "stateless", "health", "releases"}
    assert stack["stateless"] is True
    assert stack["health"]["kind"] in {"docker", "http"}
    for release in stack["releases"]:
        image, digest = release["image"].rsplit("@sha256:", 1)
        assert image and len(digest) == 64 and set(digest) <= set("0123456789abcdef")
PY

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

test_dir="$(mktemp -d)"
trap 'find "$test_dir" -depth -delete' EXIT
printf '/etc/sudoers.d/legacy: bad permissions\n/etc/sudoers: parsed OK\n' >"$test_dir/before"
printf '/etc/sudoers.d/legacy: bad permissions\n/etc/sudoers: parsed OK\n/etc/sudoers.d/lodge-agent: parsed OK\n' >"$test_dir/after"
grep -v ': parsed OK$' "$test_dir/before" >"$test_dir/before-errors"
grep -v ': parsed OK$' "$test_dir/after" >"$test_dir/after-errors"
cmp -s "$test_dir/before-errors" "$test_dir/after-errors"
printf '/etc/sudoers.d/new: syntax error\n' >>"$test_dir/after-errors"
if cmp -s "$test_dir/before-errors" "$test_dir/after-errors"; then
  printf 'sudoers baseline comparison accepted a new error\n' >&2
  exit 1
fi

printf 'install-agent policy tests: PASS\n'
