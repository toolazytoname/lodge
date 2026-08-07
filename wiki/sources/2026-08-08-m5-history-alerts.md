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
