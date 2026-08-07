---
type: source
date: 2026-08-08
status: in-progress
sources:
  - live SQLite observation history
  - M5 history and event implementation
---

# 2026-08-08 M5 历史与告警

## 有界历史查询底座

M5 首个切片先建立历史读取边界。SQLite 已保存每 30 秒的完整不可变 Observation；如果 `/api/history` 逐条加载 workload、endpoint、route 和完整 resources，120 个时间点就会重复加载数千行并把内部细节放大到浏览器。因此新增 `ObservationSummary`，只包含时间、在线/错误状态、Agent 版本、CPU/load、内存/根磁盘比例、workload/failed 数量、wildcard endpoint 数量和 warning 数量。

存储层使用一条按 host 限定的 summary 查询，默认返回最近 120 点，HTTP 边界最多允许 500 点；结果按新到旧排序。未知 host、缺失 agent、非法 limit 和未认证请求具有明确错误语义。MemStore 仅保留最多 1000 条有界摘要用于运行投影和测试，生产 SQLiteStore 从 durable history 读取。

领域测试验证资源比例和风险计数；SQLite 测试验证顺序、离线状态、失败 workload、公网绑定、warning 和上限；HTTP 测试验证 host scope 与边界错误。完整 Observation 仍保留在 SQLite，后续事件规则在服务端使用，不通过 timeline API 批量下发。

本切片只完成后端读取能力。Security 页面时间线、事件 active/acknowledged/resolved 生命周期、规则与通知仍未宣称完成。

## Security 历史趋势

第二个切片将有界历史接入 Security 页面。页面只在进入 Security 后读取当前选中主机的最近 120 点，并在 15 秒资产刷新时更新这一台；不会首屏并发下载五台完整历史。主机选择器可在五台之间切换，展示窗口在线率、按 CPU 归一化的 load、内存、根磁盘，以及 failed workload、collection warning 和 wildcard endpoint 峰值。

趋势使用原生 SVG polyline，不增加浏览器运行时依赖。缺少资源数据的离线点表现为折线间断，不被伪造成 0%；online 折线保留离线缺口。history API 独立失败时，当前公网暴露面与实时指标继续保留，历史区域显示局部错误；empty fleet 和首次 loading 也有独立语义。

完全虚构的 120 点 fixture 加入短暂离线窗口和资源波动。Playwright 在 1280 与 390 验证趋势、East 主机切换、97.5% 在线、失败峰值、无横向溢出和独立 503；新增两张人工检查的视觉基线。关键端到端场景由 10 增至 13。历史浏览完成，但路线项仍包含尚未实现的事件 transitions，因此 M5 继续进行。

## 事件生命周期底座

第三个切片建立 `active → acknowledged → resolved` 状态机。规则不直接写“告警行”，而是输出 host-scoped `EventSignal`；SQLite 将信号集合与该主机所有未恢复事件对账。新风险打开事件，持续风险只更新最后观测时间、严重度和细节，确认只记录操作者知情而不等于恢复，信号消失才恢复；同一风险未来复发会生成新 ID，旧事件保留为历史。

Observation 与事件 open/update/recovery 在同一事务提交。重复 key、跨主机、非法或时间倒退的信号会让整条观测回滚。活动 key 上的唯一索引提供数据库级第二道去重保证。事件列表上限 500；确认幂等，已恢复事件不能事后确认。

当前规则覆盖 host offline、内存、根磁盘、按 CPU 归一化 load、failed/unhealthy workload 和新增 wildcard listener。内存为 85%/80%、根磁盘为 90%/85%、load/CPU 为 1.5/1.0 的开启/解除双阈值。首次在线观测与离线后的首次恢复观测把 listener 作为基线，避免上线时全量误报；只有两个完整在线服务观测之间新增的 wildcard listener 才打开事件。离线会保留既有服务、listener 和资源风险；在线但某类采集缺失时，只保留该类既有风险，不用“没有数据”推导恢复。

领域、规则、SQLite 与 SQLiteStore 测试已覆盖去重、确认幂等、恢复、复发、阈值迟滞、listener 基线、离线/部分采集和事务回滚。事件 API、Security 事件列表、SSH 规则、冷却与 webhook 尚未完成，M5 继续进行。

## 认证事件 API

第四个切片开放认证的 `GET /api/events`，支持全 fleet 或已配置 host 筛选，默认 100、上限 500；返回 incident 类型、严重度、状态、面向操作者的说明和审计时间，不暴露内部 dedupe key。`POST /api/events/ack` 经过会话与 CSRF 校验，确认幂等，未知 ID 为 404，已恢复事件为 409。Go HTTP 契约继续生成 TypeScript union，未知 severity/state 不能静默落入浏览器。

API 集成测试使用真实 SQLite 生命周期验证未认证 401、缺 CSRF 403、active 到 acknowledged、重复确认时间不变、恢复后冲突，以及 host/limit 边界。路线图的“Observation history and event transitions”至此完成；Security 事件 UI、SSH 来源、冷却和 webhook 仍未完成。

## Security 事件中心

第五个切片用真实事件替换 Security 页的 M5 占位卡。页面默认展示所有主机的进行中事件，可按主机以及进行中、待确认、已确认、已恢复、全部事件筛选。每行展示 severity、生命周期、主机、规则类型、持续时间、最近观测与详情；`acknowledged` 仍留在进行中列表，只有 `resolved` 才进入恢复历史。确认按钮调用 CSRF 保护 API，成功后原地更新状态并明确提示“风险仍持续”。

页面首要安全指标从派生的公网 Web/离线数量调整为公网服务、待归因、待确认事件、严重进行中，其中严重数包含已确认但未恢复事件。事件 API 独立失败只让事件区域显示 N/A/局部错误，当前暴露面与 120 点历史保持可用。完全虚构 fixture 覆盖确认交互、生命周期筛选、独立事件 503，并在 1280/390 更新人工检查的视觉基线；关键端到端场景从 13 增至 17。

按设计审计保持现有暗色、绿色主强调、10/6px 圆角层级和五页 IA；事件列表使用稀疏分隔而不是新增一排通用卡片，720px 以下明确单列。没有引入第三方前端运行时。当前量化差距为 observation 规则 6/7（缺 SSH 来源）与 notification adapter 0/1。

## 可靠 Webhook 通知

第六个切片没有在采集线程里直接“发一次 HTTP”，而是建立 SQLite schema 6 的持久化 outbox。Observation、事件 opened/recovered transition 与每个已配置 channel 的投递行在同一事务提交；相同 event/transition/channel 有唯一约束。独立 worker 用 30 秒租约领取到期行，进程在 HTTP 成功后、落库前崩溃时允许再次领取，因此契约明确为 at-least-once，并用 `event-id:transition:webhook` 作为稳定的 `X-Lodge-Delivery` 幂等键。

成功 2xx 才标记 delivered；408、425、429、5xx、timeout 和 network 按 5 秒、30 秒、2 分钟、10 分钟、30 分钟、1 小时退避，最多 8 次；其他状态直接进入 failed。数据库和日志只保存 `status_NNN`、`timeout`、`network` 等小范围分类，不保存 Webhook URL、bearer、response body/header 或 raw error。客户端强制 HTTPS、禁 redirect、禁环境 proxy，body 为显式 version 1 事件投影且排除内部 dedupe key。

同一风险复发时，以此前成功发送 opened 的时间计算 30–86400 秒冷却。若新事件在延迟期间已经恢复，pending open 会被取消，并且不会制造一条从未看到 open 的 recovery；若 open 已经 leased/delivered，则 recovery 单独入队。存储、Hub 接线、配置边界、secret 文件、HTTP 状态、redirect、租约回收和冷却均有自动化测试。notification adapter 达到 1/1；M5 只剩 SSH 来源规则与真实发布验收。
