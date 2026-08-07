#!/usr/bin/env bash
# install-agent.sh — 在目标机上安装 lodge-agent。
#
# 用法（以 root 运行）：
#   ./install-agent.sh /path/to/lodge-agent        # 指定二进制
#   ./install-agent.sh                              # 默认 ./dist/lodge-agent 或当前目录
#
# 做的事：
#   1. 创建系统账号 lodge（nologin、无 home、不进 docker/wheel/sudo 组）；
#   2. 安装二进制到 /usr/local/bin/lodge-agent；
#   3. 从二进制自身生成 sudoers（单一真相来源，杜绝手写漂移）；
#   4. 校验 root-only 动作策略（缺省即禁用全部写操作）；
#   5. 安装 systemd unit 并完成真实服务上下文验收。
#
# 幂等：可重复运行。

set -euo pipefail

BIN_SRC="${1:-}"
INSTALL_DIR="/usr/local/bin"
UNIT_SRC="$(dirname "$0")/lodge-agent.service"
UNIT_DST="/etc/systemd/system/lodge-agent.service"
CONF_DIR="/etc/lodge-agent"
TOKEN_FILE="$CONF_DIR/token"
ACTION_POLICY_FILE="$CONF_DIR/actions.json"

err()  { echo "✗ $*" >&2; exit 1; }
info() { echo "  $*"; }

# ── 前置检查 ──────────────────────────────────────────────
[ "$(id -u)" -eq 0 ] || err "必须以 root 运行（sudo ./install-agent.sh）"

# 定位二进制
if [ -z "$BIN_SRC" ]; then
  for cand in ./dist/lodge-agent ./lodge-agent "$(dirname "$0")/lodge-agent"; do
    [ -f "$cand" ] && BIN_SRC="$cand" && break
  done
fi
[ -f "$BIN_SRC" ] || err "找不到 agent 二进制：$BIN_SRC（用 ./install-agent.sh /path/to/lodge-agent 指定）"
[ -x "$BIN_SRC" ] || chmod +x "$BIN_SRC"
command -v systemctl >/dev/null || err "未发现 systemd，本脚本仅支持 systemd 系统"
command -v python3 >/dev/null || err "未发现 python3，无法执行服务进程验收"
command -v visudo >/dev/null || err "未发现 visudo，无法验证 sudoers 策略"
command -v cmp >/dev/null || err "未发现 cmp，无法比较 sudoers 安全基线"
command -v stat >/dev/null || err "未发现 stat，无法验证 root-only 动作策略"
[ -f "$UNIT_SRC" ] || err "找不到 unit 文件：$UNIT_SRC"

echo "▸ 安装 lodge-agent"

# ── 1. 账号 ───────────────────────────────────────────────
if id lodge >/dev/null 2>&1; then
  info "账号 lodge 已存在，复用"
else
  # --system 系统账号；--shell /usr/sbin/nologin 禁止登录；--no-create-home
  useradd --system --shell /usr/sbin/nologin --no-create-home --home-dir /nonexistent lodge
  info "已创建系统账号 lodge（nologin）"
fi

# 安全断言：lodge 绝不能在 docker / wheel / sudo 组里。
# docker 组可挂载宿主根目录逆向提权 ≡ root；wheel/sudo 组同理。
for g in docker wheel sudo adm; do
  if id -nG lodge 2>/dev/null | tr ' ' '\n' | grep -qx "$g"; then
    err "安全检查失败：lodge 在 $g 组里（等价 root），拒绝继续。请先 gpasswd -d lodge $g"
  fi
done
info "已确认 lodge 不在任何特权组"

# ── 2. 二进制 ─────────────────────────────────────────────
install -m 0755 "$BIN_SRC" "$INSTALL_DIR/lodge-agent"
info "二进制 → $INSTALL_DIR/lodge-agent"

# ── 3. 配置目录、token 与 root-only 动作策略 ──────────────
# 目录必须由 root 拥有，否则受限 lodge 账号可 rename 掉 root-owned 策略，
# 再用自己创建的文件替换。lodge 仅通过组权限穿越目录并读取自己的 token。
[ ! -L "$CONF_DIR" ] || err "配置目录不能是符号链接：$CONF_DIR"
install -d -o root -g lodge -m 0750 "$CONF_DIR"
if [ -e "$TOKEN_FILE" ] || [ -L "$TOKEN_FILE" ]; then
  [ -f "$TOKEN_FILE" ] && [ ! -L "$TOKEN_FILE" ] || err "token 必须是普通文件且不能是符号链接"
  [ -s "$TOKEN_FILE" ] || err "现有 token 为空，拒绝静默轮换"
  chown lodge:lodge "$TOKEN_FILE"
  chmod 0600 "$TOKEN_FILE"
  info "token 已存在，按 lodge:lodge 0600 保留"
else
  python3 - "$TOKEN_FILE" "$(id -u lodge)" "$(id -g lodge)" <<'PY'
import os
import secrets
import sys

path, uid, gid = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
if hasattr(os, "O_NOFOLLOW"):
    flags |= os.O_NOFOLLOW
fd = os.open(path, flags, 0o600)
try:
    os.fchown(fd, uid, gid)
    os.write(fd, (secrets.token_hex(32) + "\n").encode("ascii"))
finally:
    os.close(fd)
PY
  info "已生成 owner-only token（内容未输出）"
fi

# 缺少策略文件是明确的 fail-closed 状态：Agent 返回空动作列表。若存在，则
# 不自动修复 owner/mode，以免把 lodge 或其他账号可控的内容提升为 root 策略。
if [ -e "$ACTION_POLICY_FILE" ] || [ -L "$ACTION_POLICY_FILE" ]; then
  [ -f "$ACTION_POLICY_FILE" ] && [ ! -L "$ACTION_POLICY_FILE" ] \
    || err "动作策略必须是普通文件且不能是符号链接：$ACTION_POLICY_FILE"
  [ "$(stat -c '%u' "$ACTION_POLICY_FILE")" = 0 ] \
    || err "动作策略必须归 root 所有：$ACTION_POLICY_FILE"
  [ "$(stat -c '%a' "$ACTION_POLICY_FILE")" = 600 ] \
    || err "动作策略权限必须精确为 0600：$ACTION_POLICY_FILE"
  "$INSTALL_DIR/lodge-agent" --list-actions >/dev/null \
    || err "动作策略格式或内容无效：$ACTION_POLICY_FILE"
  info "root-only 动作策略已验证 ✓"
else
  info "未配置动作策略：所有写操作保持禁用（fail closed）✓"
fi

# ── 4. sudoers（从二进制生成，单一真相来源）──────────────
# lodge-agent --print-sudoers 在本机 LookPath 出 docker/ss 的真实路径，
# 渲染成与 agent 内部命令逐字对应的 sudoers。
SUDOERS_FILE="/etc/sudoers.d/lodge-agent"
TMP_SUDOERS="$(mktemp)"
SUDOERS_BACKUP="$(mktemp)"
SUDOERS_BASELINE="$(mktemp)"
SUDOERS_BASELINE_ERRORS="$(mktemp)"
SUDOERS_AFTER="$(mktemp)"
SUDOERS_AFTER_ERRORS="$(mktemp)"
SUDOERS_EXISTED=0
SUDOERS_BASELINE_CLEAN=0
if visudo -c >"$SUDOERS_BASELINE" 2>&1; then
  SUDOERS_BASELINE_CLEAN=1
fi
grep -v ': parsed OK$' "$SUDOERS_BASELINE" >"$SUDOERS_BASELINE_ERRORS" || true
if ! "$INSTALL_DIR/lodge-agent" --print-sudoers > "$TMP_SUDOERS"; then
  rm -f -- "$TMP_SUDOERS" "$SUDOERS_BACKUP" "$SUDOERS_BASELINE" \
    "$SUDOERS_BASELINE_ERRORS" "$SUDOERS_AFTER" "$SUDOERS_AFTER_ERRORS"
  err "生成 sudoers 失败（agent --print-sudoers 报错，多半是本机缺 docker/ss 等命令）"
fi
# 先用 visudo 校验语法，再落地，避免坏 sudoers 卡死系统
if ! visudo -cf "$TMP_SUDOERS" >/dev/null; then
  rm -f -- "$TMP_SUDOERS" "$SUDOERS_BACKUP" "$SUDOERS_BASELINE" \
    "$SUDOERS_BASELINE_ERRORS" "$SUDOERS_AFTER" "$SUDOERS_AFTER_ERRORS"
  err "visudo 校验失败，未写入 sudoers"
fi
if [ -f "$SUDOERS_FILE" ]; then
  cp -p -- "$SUDOERS_FILE" "$SUDOERS_BACKUP"
  SUDOERS_EXISTED=1
fi
install -m 0440 -o root -g root "$TMP_SUDOERS" "$SUDOERS_FILE"
if ! visudo -c >"$SUDOERS_AFTER" 2>&1; then
  grep -v ': parsed OK$' "$SUDOERS_AFTER" >"$SUDOERS_AFTER_ERRORS" || true
  if [ "$SUDOERS_BASELINE_CLEAN" -eq 0 ] && cmp -s "$SUDOERS_BASELINE_ERRORS" "$SUDOERS_AFTER_ERRORS"; then
    info "警告：主机原有 sudoers 基线不干净；Lodge 未增加新错误，请另行修复既有策略"
  else
    if [ "$SUDOERS_EXISTED" -eq 1 ]; then
      install -m 0440 -o root -g root "$SUDOERS_BACKUP" "$SUDOERS_FILE"
    else
      rm -f -- "$SUDOERS_FILE"
    fi
    rm -f -- "$TMP_SUDOERS" "$SUDOERS_BACKUP" "$SUDOERS_BASELINE" \
      "$SUDOERS_BASELINE_ERRORS" "$SUDOERS_AFTER" "$SUDOERS_AFTER_ERRORS"
    err "完整 sudoers 策略出现新增错误，已恢复安装前 Lodge 策略"
  fi
fi
rm -f -- "$TMP_SUDOERS" "$SUDOERS_BACKUP" "$SUDOERS_BASELINE" \
  "$SUDOERS_BASELINE_ERRORS" "$SUDOERS_AFTER" "$SUDOERS_AFTER_ERRORS"
info "sudoers → $SUDOERS_FILE（候选合法，完整策略未增加错误）"

# ── 5. systemd unit ───────────────────────────────────────
install -m 0644 "$UNIT_SRC" "$UNIT_DST"
systemctl daemon-reload
info "unit → $UNIT_DST"

# ── 6. 启动 ───────────────────────────────────────────────
systemctl enable lodge-agent >/dev/null
systemctl restart lodge-agent
sleep 1
if systemctl is-active --quiet lodge-agent; then
  info "服务已启动 ✓"
else
  err "服务启动失败，查 journal：journalctl -u lodge-agent -n 30 --no-pager"
fi

# 验证真实服务进程，而不是只相信 systemctl 的配置投影。部分沙箱指令会令
# /proc/MainPID/status 出现 NoNewPrivs=1，却仍显示 NoNewPrivileges=no。
MAIN_PID="$(systemctl show lodge-agent -p MainPID --value)"
[ -n "$MAIN_PID" ] && [ "$MAIN_PID" -gt 0 ] || err "无法取得 lodge-agent MainPID"
NO_NEW_PRIVS="$(awk '/^NoNewPrivs:/ { print $2 }' "/proc/$MAIN_PID/status")"
[ "$NO_NEW_PRIVS" = 0 ] || err "服务进程 NoNewPrivs=$NO_NEW_PRIVS，sudo 白名单必然失效"
info "服务进程 NoNewPrivs=0（允许固定 sudo 白名单）✓"

# ── 验证采集（非 root 跑一次，证明降权真实有效）──────────
if sudo -u lodge "$INSTALL_DIR/lodge-agent" --check >/dev/null 2>&1; then
  info "lodge 账号 --check 通过（采集无需直接 root）✓"
else
  err "lodge --check 失败；拒绝留下无法采集的服务"
fi

# 请求真实 systemd 服务，让 Agent 在自身沙箱里执行采集。token 仅由 lodge
# 进程从 owner-only 文件读取，不进入命令参数或输出。
if sudo -u lodge python3 - "$TOKEN_FILE" <<'PY'
import json
import sys
import urllib.request

with open(sys.argv[1], encoding="utf-8") as stream:
    token = stream.read().strip()

payloads = {}
for path in ("/v1/status", "/v1/services", "/v1/actions"):
    request = urllib.request.Request(
        "http://127.0.0.1:9101" + path,
        headers={"Authorization": "Bearer " + token},
    )
    with urllib.request.urlopen(request, timeout=10) as response:
        payloads[path] = json.load(response)

warnings = payloads["/v1/status"].get("warnings", []) + payloads["/v1/services"].get("warnings", [])
privilege_warnings = [
    warning for warning in warnings
    if "sudo" in warning.lower() or "no new privileges" in warning.lower()
]
services = payloads["/v1/services"].get("services", [])
ssh = payloads["/v1/status"].get("ssh")
actions = payloads["/v1/actions"].get("actions")
if privilege_warnings:
    raise SystemExit("service-context collection has privilege warnings")
if not services:
    raise SystemExit("service-context collection found no services")
if not isinstance(ssh, dict) or not isinstance(ssh.get("failedTotal"), int) or not isinstance(ssh.get("sources"), list):
    raise SystemExit("service-context SSH authentication summary is missing or invalid")
if not isinstance(actions, list):
    raise SystemExit("service-context controlled action list is missing or invalid")
print(f"  服务进程采集通过：services={len(services)} warnings={len(warnings)} ssh_failures={ssh['failedTotal']} actions={len(actions)} ✓")
PY
then
  :
else
  err "真实服务进程采集验收失败"
fi

# ── 越权验证：任意命令、旧直连写操作和动态参数均须拒绝 ───
if sudo -u lodge sudo -n docker run --rm hello-world >/dev/null 2>&1 \
   || sudo -u lodge sudo -n -n docker run --rm alpine cat /etc/shadow >/dev/null 2>&1 \
   || sudo -u lodge sudo -n docker system prune -f >/dev/null 2>&1 \
   || sudo -u lodge sudo -n journalctl --vacuum-time=7d >/dev/null 2>&1 \
   || sudo -u lodge sudo -n systemctl restart caddy >/dev/null 2>&1 \
   || sudo -u lodge sudo -n "$INSTALL_DIR/lodge-agent" --execute-action restart:systemd:caddy.service >/dev/null 2>&1; then
  err "安全验证失败：lodge 命中了未批准命令、旧直连写操作或动态参数"
else
  info "越权验证：任意命令、旧直连写操作与动态参数均被拒绝 ✓"
fi

echo
echo "▸ 完成。下一步："
echo "    1. 按 docs/agent-onboarding.md 将 owner-only token 安全导入 Hub（不要打印或放进命令参数）"
echo "    2. 如需受控动作，审阅 deploy/agent-actions.example.json 后以 root:root 0600 安装为 $ACTION_POLICY_FILE"
echo "    3. 配置 tailnet-only Serve：sudo deploy/tailnet-management.sh apply agent"
echo "    4. 从 Hub 验证 tailnet 路由、采集状态和动作清单"
