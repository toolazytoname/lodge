---
type: source
status: in-progress
sources:
  - "[[lodge-server-management-roadmap]]"
---

# M7 声明式部署

## Agent 主机事务

Agent `0.7.0` 建立独立的 root-owned `deployments.json`。缺少策略即零权限；版本
1 只接受明确声明为无状态的单 Compose service、完整 sha256 镜像摘要，以及 Docker
health 或 `127.0.0.1` HTTP health。路径、服务、镜像、健康地址和超时都由 root 预先
登记，Hub/浏览器只会看到并选择 release ID。

项目目录、Compose、可选 `.env` 及完整路径链必须 root-owned、无符号链接且不可由
group/world 写入。执行只使用固定 Docker/Compose argv，不经过 Shell；只更新登记
service 且带 `--no-deps`。策略和 Agent 结果都不能携带命令或环境，Compose/Docker
原始输出、健康响应和错误也不越过 root 边界。

首次部署前从运行容器发现 immutable RepoDigest；没有摘要就拒绝。当前/上一版本
metadata 与精确生成的 override 合成一个 root-only canonical 文件，读取时重新生成
全文比对，避免 state/override 崩溃漂移。候选验证通过后才 fsync + atomic rename。
apply、health 或 commit 失败时会重新应用操作前镜像并再次 health；恢复成功仍保留
原失败分类并标记 `rollbackPerformed`，恢复失败升级为 `rollback_failed`。

## 量化状态

Agent 安全门禁 8/8：root policy、无 Shell、stateless-only、immutable digest、
root-owned Compose paths、local health、persistent rollback point、verified automatic
rollback。macOS 单元测试、Linux 测试编译、Linux Agent 构建、安装器策略测试与
vet 已通过；完整门禁和 GitHub CI 将作为本切片提交证据。

## Hub 与 Web 闭环

Hub `0.8.0` 新增实时发布清单和异步执行 API。list 与 execute 都重新访问 Agent，
执行只接收 agent/deployment ID/逐字确认，不接受镜像、路径、Compose、环境或健康
目标。普通动作和发布共享全局 admission lock；Hub 在返回 HTTP 202 前以 compare-and-set
持久化 `requested → running`，随后在独立有界 context 中只发送一次 Agent POST。
浏览器断开不会取消已受理事务，Hub 重启也不会重放不确定执行。

Agent 成功恢复操作前版本时，Hub 保存 `rolled_back` 与原始失败分类；恢复失败保存
`failed/rollback_failed`。Operations 页面展示当前/上一 release、缩短摘要、部署/回滚
风险与安全边界，精确确认后立即显示“已受理”，再轮询持久审计直到 terminal。fixture
覆盖成功、失败、已回滚、CSRF、错误确认、共享串行锁和 390/1280 视觉，无真实主机、
地址或镜像进入截图。

量化状态更新为 Agent 安全控制 8/8、发布 terminal audit 3/3、Hub 边界回归测试
51/51、关键端到端场景 26。代码与自动化验收已完成；production 空策略滚动和首个
经人工确认的无状态 stack/digest 仍未完成，因此 M7 总体保持进行中。Lodge 不会从
现有业务容器自动猜测第一个发布目标。
