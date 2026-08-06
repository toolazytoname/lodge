# Wiki Log

## [2026-08-06] query | Lodge 服务器统一管理与安全路线

- 审阅当前 hub/agent 实现与部署脚本。
- 对 bytebunny、bytedragon、Ali、tencent、banwagong 做只读服务与 SSH 安全基线检查。
- 创建 [[2026-08-06-lodge-readonly-audit]]。
- 创建 [[lodge-server-management-roadmap]]。

## [2026-08-07] remediation | Hub 管理面改为 Tailnet-only

- 新增可测试的 Tailscale Serve/Funnel 检查与迁移脚本、grants 模板和恢复流程。
- bytedragon 保留 SSH 恢复连接并保存变更前状态后，从公网 Funnel 切换为私有 Serve。
- 验证 `AllowFunnel` 为 0、Serve 指向 loopback Hub，本机通过 Tailnet 可访问会话 API。
- 将 `public_management_endpoints` 质量指标从 1 更新为 0。

## [2026-08-07] implementation | 持久化领域模型与 SQLite 底座

- 建立 Host、Workload、Endpoint、Observation、Event、Operation 的纯领域契约，并在 Hub 边界投影 Agent v1 数据。
- 将套接字绑定与经证据确认的可达性拆分，拒绝没有来源和检查时间的非 unknown 可达性。
- 新增 CGo-free SQLite 适配器和 v1 严格 schema，规范化保存观测、workload、endpoint、注解、事件和操作审计；Agent URL/token 不进入数据库。
- 迁移清单要求连续编号并记录 SHA-256 校验和；数据库版本超前或迁移账本被篡改时拒绝启动。
- 备份使用 `VACUUM INTO`，通过完整性和 schema 版本校验后才成功；数据库及 WAL/SHM/备份均要求 `0600`。
- 单元、竞态、无 CGo Linux 构建和漏洞扫描通过；Hub 接线与旧 JSON 状态迁移仍是 M2 后续工作，尚未宣称生产历史功能上线。

## [2026-08-07] implementation | Hub 切换到 SQLite 持久化

- Hub 生产入口改为 SQLiteStore：每次完整、部分或离线采集先写不可变观测，再更新当前 Web 投影；写入错误不再静默忽略。
- 旧 MemStore 删除 JSON 落盘能力，Agent URL/token 只保留于 `0600` 私有配置和进程内存。
- schema v2 增加注解导入账本；旧 `state.json` 按文件摘要只导入一次注解，忽略 Agent 连接记录，现有 SQLite 注解优先。
- 新增真实 v1→v2 迁移测试、未知/缺失 Agent API 版本拒绝、30 天观测裁剪、优雅关闭和注解失败 500 处理。
- `lodge-hub --backup` 只接受已存在的初始化数据库，不迁移源库，通过 `VACUUM INTO`、完整性与源/备份版本一致性检查后成功。
- 单元、静态分析、竞态、无 CGo Linux 构建、CLI 错误路径和漏洞扫描全部通过；生产 Hub 尚未部署本提交，因此运行时保留天数仍记为 0。
