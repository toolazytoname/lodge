#!/usr/bin/env bash
# bootstrap-lodge-canary.sh — 为 Lodge M7 创建隔离、无状态的真实发布 canary。
#
# 以 root 在一台已经安装 Agent 0.7+ 的 Docker 主机执行。此脚本拒绝覆盖
# 任何已有 Lodge 部署策略或 /srv/lodge-canary 目录；它不开放公网端口。
set -euo pipefail

readonly PROJECT_DIR="/srv/lodge-canary"
readonly COMPOSE_FILE="$PROJECT_DIR/compose.yaml"
readonly POLICY_FILE="/etc/lodge-agent/deployments.json"
readonly AGENT_BIN="/usr/local/bin/lodge-agent"
readonly LOOPBACK_PORT="18080"
readonly V1_IMAGE="nginx@sha256:814a8e88df978ade80e584cc5b333144b9372a8e3c98872d07137dbf3b44d0e4"
readonly V2_IMAGE="nginx@sha256:4ff102c5d78d254a6f0da062b3cf39eaf07f01eec0927fd21e219d0af8bc0591"

die() { printf '✗ %s\n' "$*" >&2; exit 1; }
info() { printf '▸ %s\n' "$*"; }

[ "$(id -u)" -eq 0 ] || die "必须以 root 运行"
command -v docker >/dev/null || die "未找到 docker"
docker compose version >/dev/null || die "未找到 docker compose plugin"
command -v curl >/dev/null || die "未找到 curl，无法验证 loopback 健康检查"
[ -x "$AGENT_BIN" ] || die "未找到已安装的 Lodge Agent：$AGENT_BIN"
[ ! -e "$PROJECT_DIR" ] && [ ! -L "$PROJECT_DIR" ] || die "拒绝覆盖现有目录：$PROJECT_DIR"
[ ! -e "$POLICY_FILE" ] && [ ! -L "$POLICY_FILE" ] || die "拒绝覆盖已有部署策略：$POLICY_FILE"

tmp_dir="$(mktemp -d)"
trap 'rm -rf -- "$tmp_dir"' EXIT

cat >"$tmp_dir/compose.yaml" <<EOF
services:
  canary:
    image: ${V1_IMAGE}
    ports:
      - "127.0.0.1:${LOOPBACK_PORT}:80"
    mem_limit: 64m
    pids_limit: 32
    restart: "no"
    healthcheck:
      test: ["CMD-SHELL", "wget -q -O /dev/null http://127.0.0.1/ || exit 1"]
      interval: 5s
      timeout: 3s
      retries: 6
      start_period: 5s
EOF

cat >"$tmp_dir/deployments.json" <<EOF
{
  "version": 1,
  "stacks": [
    {
      "key": "lodge-canary",
      "label": "Lodge isolated Nginx canary",
      "projectDirectory": "${PROJECT_DIR}",
      "composeFile": "${COMPOSE_FILE}",
      "service": "canary",
      "stateless": true,
      "health": {
        "kind": "http",
        "url": "http://127.0.0.1:${LOOPBACK_PORT}/",
        "timeoutSeconds": 60
      },
      "releases": [
        {
          "id": "nginx-1.27.3-alpine",
          "label": "Nginx 1.27.3 Alpine",
          "image": "${V1_IMAGE}"
        },
        {
          "id": "nginx-1.27.4-alpine",
          "label": "Nginx 1.27.4 Alpine",
          "image": "${V2_IMAGE}"
        }
      ]
    }
  ]
}
EOF

install -d -o root -g root -m 0755 "$PROJECT_DIR"
install -o root -g root -m 0644 "$tmp_dir/compose.yaml" "$COMPOSE_FILE"
info "拉取并启动 immutable bootstrap release"
docker compose --project-directory "$PROJECT_DIR" -f "$COMPOSE_FILE" up -d --no-build

deadline=$((SECONDS + 60))
until curl --fail --silent --show-error "http://127.0.0.1:${LOOPBACK_PORT}/" >/dev/null; do
  [ "$SECONDS" -lt "$deadline" ] || die "bootstrap canary 未通过 loopback 健康检查；保留目录供排查，未写入 Lodge 部署策略"
  sleep 2
done

install -d -o root -g root -m 0750 "$(dirname "$POLICY_FILE")"
install -o root -g root -m 0600 "$tmp_dir/deployments.json" "$POLICY_FILE"
"$AGENT_BIN" --list-deployments >/dev/null || die "部署策略验证失败；请移除 $POLICY_FILE 后检查 root-owned Compose 路径"

info "canary 已就绪：仅 127.0.0.1:${LOOPBACK_PORT}，64 MiB，零数据卷"
info "在 Lodge Operations 选择 banwagong 的 Nginx 1.27.4 Alpine，输入其精确确认短语以执行首次受控发布。"
