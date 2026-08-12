#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

updater=deploy/lodge-upgrade-cliproxyapi
installer=deploy/install-cliproxyapi-updater.sh
bash -n "$updater"
bash -n "$installer"

required_updater_patterns=(
  'readonly REPOSITORY="router-for-me/CLIProxyAPI"'
  '[[ $# -eq 0 ]] || fail "arguments are not accepted"'
  "unset ALL_PROXY HTTPS_PROXY HTTP_PROXY NO_PROXY all_proxy https_proxy http_proxy no_proxy"
  "--proto '=https'"
  '[[ "sha256:${actual_archive_digest}" == "${archive_digest}" ]]'
  'official checksums.txt does not match the archive'
  'tar -xOzf "${archive}" cli-proxy-api'
  '[[ "${candidate_version}" == "CLIProxyAPI Version: ${version},"* ]]'
  'systemctl stop "${SERVICE}"'
  'wait_for_health 60 || rollback'
  'UPGRADE ROLLED BACK'
  'ROLLBACK FAILED'
)
for pattern in "${required_updater_patterns[@]}"; do
  grep -F -- "$pattern" "$updater" >/dev/null || {
    printf 'CLIProxyAPI updater policy missing: %s\n' "$pattern" >&2
    exit 1
  }
done

required_installer_patterns=(
  'root execution is required'
  'lodge-admin does not exist'
  'visudo -cf "${candidate}"'
  'NOPASSWD: %s ""'
  'sudo -u "${ADMIN_USER}" sudo -n -l "${TARGET}" unexpected'
  'arguments=denied'
)
for pattern in "${required_installer_patterns[@]}"; do
  grep -F -- "$pattern" "$installer" >/dev/null || {
    printf 'CLIProxyAPI updater installer policy missing: %s\n' "$pattern" >&2
    exit 1
  }
done

if grep -Eq '\beval\b|bash[[:space:]]+-c|source[[:space:]]' "$updater"; then
  printf 'CLIProxyAPI updater contains a shell-capable execution path\n' >&2
  exit 1
fi

chmod +x "$updater" "$installer"
if "$updater" unexpected >/dev/null 2>&1; then
  printf 'CLIProxyAPI updater accepted an argument\n' >&2
  exit 1
fi

printf 'CLIProxyAPI updater policy tests: PASS\n'
