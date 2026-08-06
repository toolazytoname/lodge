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
#   4. 安装 systemd unit（加固：见 lodge-agent.service 注释）；
#   5. 启动服务，token 首次启动时由 agent 写入 /etc/lodge-agent/token。
#
# 幂等：可重复运行。

set -euo pipefail

BIN_SRC="${1:-}"
INSTALL_DIR="/usr/local/bin"
UNIT_SRC="$(dirname "$0")/lodge-agent.service"
UNIT_DST="/etc/systemd/system/lodge-agent.service"
CONF_DIR="/etc/lodge-agent"
TOKEN_FILE="$CONF_DIR/token"

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

# ── 3. 配置目录与 token ───────────────────────────────────
install -d -o lodge -g lodge -m 0750 "$CONF_DIR"
# 若已有 token，保留（重装不应让旧 token 失效）
if [ -f "$TOKEN_FILE" ]; then
  info "token 已存在，保留"
else
  info "token 将在服务首次启动时由 agent 生成"
fi

# ── 4. sudoers（从二进制生成，单一真相来源）──────────────
# lodge-agent --print-sudoers 在本机 LookPath 出 docker/ss 的真实路径，
# 渲染成与 agent 内部命令逐字对应的 sudoers。
SUDOERS="$INSTALL_DIR/lodge-agent --print-sudoers"
SUDOERS_FILE="/etc/sudoers.d/lodge-agent"
TMP_SUDOERS="$(mktemp)"
if ! "$INSTALL_DIR/lodge-agent" --print-sudoers > "$TMP_SUDOERS"; then
  rm -f "$TMP_SUDOERS"
  err "生成 sudoers 失败（agent --print-sudoers 报错，多半是本机缺 docker/ss 等命令）"
fi
# 先用 visudo 校验语法，再落地，避免坏 sudoers 卡死系统
if command -v visudo >/dev/null; then
  if ! visudo -cf "$TMP_SUDOERS" >/dev/null; then
    rm -f "$TMP_SUDOERS"
    err "visudo 校验失败，未写入 sudoers"
  fi
fi
install -m 0440 -o root -g root "$TMP_SUDOERS" "$SUDOERS_FILE"
rm -f "$TMP_SUDOERS"
info "sudoers → $SUDOERS_FILE（由 agent 生成并经 visudo 校验）"

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

# ── 验证采集（非 root 跑一次，证明降权真实有效）──────────
if sudo -u lodge "$INSTALL_DIR/lodge-agent" --check >/dev/null 2>&1; then
  info "lodge 账号 --check 通过（采集无需 root）✓"
else
  echo "  ! lodge --check 有 warning（可能 sudoers/docker 未就绪），详见：sudo -u lodge $INSTALL_DIR/lodge-agent --check"
fi

# ── 越权验证：lodge 不能 docker run 提权 ──────────────────
if sudo -u lodge sudo -n docker run --rm hello-world >/dev/null 2>&1 \
   || sudo -u lodge sudo -n -n docker run --rm alpine cat /etc/shadow >/dev/null 2>&1; then
  err "安全验证失败：lodge 能跑 docker run —— sudoers 白名单可能有漏洞"
else
  info "越权验证：lodge 无法 docker run（白名单生效）✓"
fi

echo
echo "▸ 完成。下一步："
echo "    1. 按 docs/agent-onboarding.md 将 owner-only token 安全导入 Hub（不要打印或放进命令参数）"
echo "    2. 配置 tailnet-only Serve：sudo deploy/tailnet-management.sh apply agent"
echo "    3. 从 Hub 验证 tailnet 路由和采集状态"
