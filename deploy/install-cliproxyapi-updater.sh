#!/usr/bin/env bash
# One-time root bootstrap for the narrow, no-argument CLIProxyAPI updater.

set -Eeuo pipefail

readonly SOURCE="${1:-$(dirname "$0")/lodge-upgrade-cliproxyapi}"
readonly TARGET="/usr/local/sbin/lodge-upgrade-cliproxyapi"
readonly SUDOERS="/etc/sudoers.d/lodge-cliproxyapi-updater"
readonly ADMIN_USER="lodge-admin"

fail() { printf 'INSTALL FAILED: %s\n' "$1" >&2; exit 1; }
[[ ${EUID} -eq 0 ]] || fail "root execution is required"
[[ $# -le 1 ]] || fail "usage: install-cliproxyapi-updater.sh [updater-path]"
[[ -f "${SOURCE}" && ! -L "${SOURCE}" ]] || fail "updater source is missing or unsafe"
id "${ADMIN_USER}" >/dev/null 2>&1 || fail "lodge-admin does not exist"
command -v visudo >/dev/null 2>&1 || fail "visudo is unavailable"
bash -n "${SOURCE}" || fail "updater syntax is invalid"
"${SOURCE}" --self-check >/dev/null || fail "updater self-check failed"

candidate="$(mktemp)"
baseline="$(mktemp)"
after="$(mktemp)"
backup_dir="$(mktemp -d)"
cleanup() { rm -rf -- "${candidate}" "${baseline}" "${after}" "${backup_dir}"; }
trap cleanup EXIT

visudo -c >"${baseline}" 2>&1 || true
printf '%s ALL=(root) NOPASSWD: %s ""\n' "${ADMIN_USER}" "${TARGET}" >"${candidate}"
chmod 0440 "${candidate}"
visudo -cf "${candidate}" >/dev/null || fail "candidate sudoers policy is invalid"

if [[ -f "${TARGET}" ]]; then cp -p -- "${TARGET}" "${backup_dir}/updater"; fi
if [[ -f "${SUDOERS}" ]]; then cp -p -- "${SUDOERS}" "${backup_dir}/sudoers"; fi
restore_previous() {
  if [[ -f "${backup_dir}/updater" ]]; then install -o root -g root -m 0755 "${backup_dir}/updater" "${TARGET}"; else rm -f -- "${TARGET}"; fi
  if [[ -f "${backup_dir}/sudoers" ]]; then install -o root -g root -m 0440 "${backup_dir}/sudoers" "${SUDOERS}"; else rm -f -- "${SUDOERS}"; fi
}
fail_installed() {
  restore_previous
  fail "$1"
}
install -o root -g root -m 0755 "${SOURCE}" "${TARGET}"
install -o root -g root -m 0440 "${candidate}" "${SUDOERS}"

if ! visudo -c >"${after}" 2>&1; then
  fail_installed "complete sudoers validation failed; previous state restored"
fi

sudo -u "${ADMIN_USER}" sudo -n -l "${TARGET}" >/dev/null 2>&1 || fail_installed "exact updater command is not authorized"
if sudo -u "${ADMIN_USER}" sudo -n -l "${TARGET}" unexpected >/dev/null 2>&1; then
  fail_installed "updater sudoers unexpectedly accepts arguments"
fi

printf 'INSTALL SUCCEEDED\ncommand=sudo -n %s\narguments=denied\n' "${TARGET}"
