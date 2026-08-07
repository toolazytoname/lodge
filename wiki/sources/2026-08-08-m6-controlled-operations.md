---
type: source
status: complete
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

## Hub、审计与 Web 闭环

Hub `0.7.0` 不缓存执行权限：列出动作与真正执行都会访问 Agent 当前策略；浏览器
只能发送 agent ID、action ID 和逐字确认语，CSRF、JSON 字段、query、大小与身份
都在边界验证。全 fleet 同时只允许一个动作，Agent 的非幂等 POST 禁 redirect、
环境 proxy 和重试；丢失响应只会形成分类失败，绝不会自动重放。

SQLite 复用 schema 1 已有的 operation 表，按 compare-and-set 保存
`requested → running → succeeded|failed`。操作者是每次登录变化、不可逆推出 cookie
的会话指纹；持久记录只有 host、目标、动作、时间、摘要和 error category。Agent
日志只存在于本次认证响应，secret sentinel 测试证明不进入 DB/WAL/SHM。Hub 启动
时把中断记录改为 `failed/hub_restarted`，不猜测远程结果，也不重试。

Operations 页面现在展示选中主机的实时 root policy、风险级别、Agent 同步、逐字
确认对话框、瞬时结果与完整审计；390/1280 截图和异常路径均纳入门禁。

## 当前边界

`a650939` 完成功能切片后，Linux CI 发现 Operations 页面在不同字体度量下有 5px
高度差；`4119a48` 固定完整画布高度而不裁图、不放宽阈值，push/PR 双 CI、race、
Chromium 和漏洞扫描全部通过。

五台主机随后通过保留 token、sudoers、unit、策略与旧二进制回滚点的事务流程统一
升级到 Agent `0.6.0`；动作数量为 3/3/0/3/13。核心反代只批准
logs/start/restart，不批准 stop；Ali 保持空策略；banwagong 的 Postgres/Redis 只批准
logs，应用容器才具备 stop。Hub `0.7.0` 通过既有安装器保留一致性备份与回滚包后
上线，schema 仍为 7。

生产动作验收保留了三条真实审计：Caddy 日志超过 64 KiB，安全失败为
`log_read_failed` 且未重试；已运行 Caddy 的幂等 start 成功，前后均为 running；
Redis 日志成功返回 200 行，实际最长样本未出现在 DB/WAL/SHM。终验 5/5 online、
55 workloads、86 endpoints、11 routes、0 warnings、0 unidentified，最大观测年龄
24.6 秒，2 succeeded、1 failed、0 interrupted。M6 完成，下一阶段进入 M7 声明式部署。
