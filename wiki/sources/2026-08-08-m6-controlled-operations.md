---
type: source
status: active
sources:
  - "[[lodge-server-management-roadmap]]"
---

# M6 受控运维动作

## Agent 权限边界切片

Agent `0.6.0` 将旧原型中的 Docker prune、journal vacuum 和直接 Caddy
restart sudo 条目全部移除。sudoers 的动作侧现在只有一个精确
`lodge-agent --execute-action`，并以另一个精确只读入口列出策略投影；追加动作
ID 或其他 argv 均不能命中授权。

每台主机的 `/etc/lodge-agent/actions.json` 必须是 root-owned、mode `0600`、
非符号链接普通文件，父目录也由 root 拥有且不可由 `lodge` 写入。缺少策略即
返回空清单。策略只能列出严格语法的 systemd service 或 Docker container
身份，以及各自允许的 start/stop/restart/logs 子集，不能写命令、路径、参数或
环境变量。

root helper 从有界 stdin 读取一个 action ID，重新解析策略，以固定 argv 执行，
并通过 root-owned 非阻塞锁禁止并行动作。状态变更记录前后 running/stopped，
最长 15 秒并在 10 秒内验证目标状态；日志最多 200 行/64 KiB，做 UTF-8、控制
字符和常见凭据模式清理。原始 stderr、进程错误和 argv 不越过 Agent HTTP
边界，日志也不会进入后续持久审计。

安装器同时修正策略替换边界：`/etc/lodge-agent` 从 `lodge` 所有改为
`root:lodge 0750`，token 由安装器预生成或保留为 `lodge:lodge 0600`，服务在
`ProtectSystem=strict` 下不再拥有目录写权限。现有策略若 owner/mode/type 不
安全会直接阻断安装，而不是自动“修复”为可信。服务上下文验收新增动作清单，
并负向验证任意 Docker run、三条旧直连写操作及 helper 动态 argv 全部被拒。

## 当前边界

这一切片只完成 Agent capability 与部署约束，尚未把能力接到 Hub。Hub 代理、
CSRF、确认短语、`requested → running → success/fail` 持久审计、Web 操作页、
五机发布和低风险 live 验收完成前，M6 不能标记完成。
