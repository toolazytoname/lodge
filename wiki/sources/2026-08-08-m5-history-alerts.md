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
