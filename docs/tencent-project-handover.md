# Tencent legacy project handover

`tencent` 的旧交互身份是 `dev`，而当前唯一的远程管理身份是
`lodge-admin`。两者是独立 Linux UID；新管理员看不到旧用户 tmux socket
是预期的隔离行为，不应通过恢复 `dev` 的 SSH、共享 Docker 组或授予全局 sudo
来解决。

本交接只适用于 `/home/dev/claude-app`。它保留项目所有者 `dev`，仅以 POSIX
ACL 给予 `lodge-admin` 项目读写及目录遍历权限，并为所有项目子目录设置默认
ACL，使新文件继承协作权限。

## 前置条件

- 已在 provider console 确认恢复路径，且以 root 执行。
- `dev` 与 `lodge-admin` 都存在；项目路径是由 `dev` 所有的真实目录，而不是
  符号链接。
- Debian 12 上已安装 `acl` 软件包。脚本拒绝自动安装软件包，以避免在未审阅的
  主机上隐式改变系统包状态。

## 执行与回退

将仓库中经过审阅的
[`grant-tencent-claude-app-acl.sh`](../deploy/grant-tencent-claude-app-acl.sh)
传入 root console 后执行。它先在 `/root/lodge-acl-backups/` 生成 `0600` 的
ACL 备份，再应用授权并以 `lodge-admin` 身份验证项目根目录读写能力。

如需回退，root 使用脚本输出的备份路径执行：

```bash
setfacl --restore=/root/lodge-acl-backups/claude-app-<timestamp>.acl
```

该流程不改变 OpenSSH、Tailscale grants、sudoers、用户组、`/home/dev` 其他路径
或 Docker 权限。
