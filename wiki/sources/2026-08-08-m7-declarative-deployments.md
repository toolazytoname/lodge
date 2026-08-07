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

## Fail-closed 生产滚动

首轮 CI 在 Linux 先后揭示桌面/移动 Operations 完整页面分别比 macOS 短 6px/7px；
修复仅固定 1765px/2548px 完整画布，没有裁图或放宽像素阈值。最终提交的 push CI
[31221180729](https://github.com/toolazytoname/lodge/actions/runs/31221180729) 与 PR CI
[31221182851](https://github.com/toolazytoname/lodge/actions/runs/31221182851) 均通过完整质量
门禁、6 组 Chromium 场景和 `govulncheck`。

Go 1.26.5、CGo-free Linux amd64 Agent `0.7.0` SHA-256 为
`9241549d5070ea01e28d6c3aaefd3fb22a3fdd29aa29effd0d7b02d447289fdb`。
五台逐机事务滚动保留 token/动作策略摘要，验证真实服务、精确 sudo helper、任意命令/
动态参数拒绝和无策略空清单；动作数保持 3/3/0/3/13，合计 22。每台
`deployments.json` 都不存在，root-only 状态目录为空，Hub 路径读取均为
Agent 0.7.0/deployments=0。回滚包为：

- bytebunny：`/var/lib/lodge-deploy-backups/agent-20260807T214753Z-8nZwzI`
- bytedragon：`/var/lib/lodge-deploy-backups/agent-20260807T214852Z-zAWQ5S`
- Ali：`/var/lib/lodge-deploy-backups/agent-20260807T214901Z-RLoIWI`
- tencent：`/var/lib/lodge-deploy-backups/agent-20260807T215020Z-ZOoy3B`
- banwagong：`/var/lib/lodge-deploy-backups/agent-20260807T215053Z-z2m4qQ`

bytebunny 与 banwagong 的全局 sudoers 基线原本不干净，安装器全文比较确认 Lodge 没有
增加新错误；这是保留的宿主债务。tencent 的 SSH 横幅会破坏 scp 协议，首轮在写入前
停止并清理空 staging，随后用 root-only stdin 传输完成，未修改横幅配置。所有五台
staging 最终为零。

Hub `0.8.0` SHA-256 为
`d98e44a0bd94a2e34042d926eb7f6bdfc9ab5d8b5d1b058bb242fe4cb8b28d25`；事务回滚包
`/var/lib/lodge-deploy-backups/hub-20260807T215206Z-rje9j0`，发布后一致性备份
`/var/lib/lodge-hub/backups/post-deploy-20260807T215211Z-3946316.db`。新发布 API 未认证
均为 401，嵌入资源与 Tailnet-only HTTPS Serve 正常。终验为 schema 7/integrity ok、
5/5 online、55 workloads、86 endpoints、11 routes、0 warnings、0 unidentified、最大
观测年龄 24.5 秒、0 in-flight、既有 3 条 terminal operation。五枚 Agent token 在
SQLite/WAL/SHM 均无命中，发布后 Hub 日志无 fatal/panic/敏感模式。

这是 M7 平台的 production fail-closed 验收，不是业务发布验收。首个真实 stack 必须
由操作者确认无状态属性、Compose 路径、immutable digest、health 与 recovery plan；
在此之前 production deploy/rollback 次数保持 0，M7 总体仍为 in-progress。

## 当前 stack 合格性

上线后只读检查全部 Compose identity，共 3 个且都在 banwagong。PostgreSQL 与 Redis
属于明确的有状态组件。`new-api` 有 Docker health，运行镜像也有一个 immutable
RepoDigest，但容器具有两个可写 bind mount：`/data` 与 `/app/logs`。在没有业务语义、
数据恢复流程和操作者声明前，`/data` 必须按持久状态处理。因此当前合格的 M7 v1
production target 为 0。

最后 live 验收有两个安全路径：新增一个隔离、无公网端口、无可写数据挂载的 stateless
canary stack；或先重构/证明 `new-api` 的状态与恢复语义，再单独批准。二者都会改变生产
工作负载或数据边界，不能由自动化代替操作者选择。
