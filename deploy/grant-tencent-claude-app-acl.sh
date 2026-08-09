#!/usr/bin/env bash
# grant-tencent-claude-app-acl.sh — 将 tencent 的单个旧项目交接给 lodge-admin。
#
# 此脚本刻意只允许 /home/dev/claude-app。它不会改变 SSH、sudo、用户组、
# Docker 权限或 /home/dev 的其他内容。以 root 在 tencent provider console 执行。
set -euo pipefail

readonly PROJECT_DIR="/home/dev/claude-app"
readonly LEGACY_USER="dev"
readonly ADMIN_USER="lodge-admin"
readonly BACKUP_ROOT="/root/lodge-acl-backups"

die() { printf '✗ %s\n' "$*" >&2; exit 1; }
info() { printf '▸ %s\n' "$*"; }

[ "$(id -u)" -eq 0 ] || die "必须以 root 运行"
command -v setfacl >/dev/null || die "未找到 setfacl；请先由 root 安装 Debian 的 acl 软件包"
command -v getfacl >/dev/null || die "未找到 getfacl；请先由 root 安装 Debian 的 acl 软件包"
id "$LEGACY_USER" >/dev/null || die "旧账户不存在：$LEGACY_USER"
id "$ADMIN_USER" >/dev/null || die "管理员账户不存在：$ADMIN_USER"
[ -d "$PROJECT_DIR" ] && [ ! -L "$PROJECT_DIR" ] || die "项目目录必须是非符号链接目录：$PROJECT_DIR"
[ "$(stat -c '%U' "$PROJECT_DIR")" = "$LEGACY_USER" ] || die "拒绝修改非 $LEGACY_USER 所有的项目目录"

install -d -o root -g root -m 0700 "$BACKUP_ROOT"
backup_path="$BACKUP_ROOT/claude-app-$(date -u +%Y%m%dT%H%M%SZ).acl"
umask 077
getfacl --absolute-names --physical --recursive "$PROJECT_DIR" >"$backup_path"
chmod 0600 "$backup_path"

# rwX grants write to files that were already writable by their owner and full
# traversal/write access to directories. Default ACLs are applied only to
# directories so future project files inherit the intended collaboration path.
setfacl --physical --recursive --modify "u:${ADMIN_USER}:rwX,m::rwX" "$PROJECT_DIR"
while IFS= read -r -d '' directory; do
  setfacl --physical --modify "d:u:${ADMIN_USER}:rwX,d:m::rwX" "$directory"
done < <(find "$PROJECT_DIR" -type d -print0)

sudo -u "$ADMIN_USER" test -r "$PROJECT_DIR" || die "管理员无法读取项目目录"
sudo -u "$ADMIN_USER" test -w "$PROJECT_DIR" || die "管理员无法写入项目目录"
getfacl --absolute-names --physical "$PROJECT_DIR" | grep -Fx "user:${ADMIN_USER}:rwx" >/dev/null \
  || die "项目根目录缺少管理员 ACL"
getfacl --absolute-names --physical "$PROJECT_DIR" | grep -Fx "default:user:${ADMIN_USER}:rwx" >/dev/null \
  || die "项目根目录缺少管理员默认 ACL"

info "已仅向 ${ADMIN_USER} 授予 ${PROJECT_DIR} 的协作 ACL"
info "原 ACL 备份：${backup_path}（如需回退：setfacl --restore=<该文件>）"
