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

## [2026-08-07] deployment | bytedragon Hub 迁移到 SQLite

- 部署提交 `d2a13a5` 的静态 x86-64 Hub，二进制 SHA-256 为 `dabfadead53668d369a522dc80267f595b13a6c83e76befdde227a4464b24318`。
- 事务安装器在停 Hub 后创建 root-only 回滚包 `/var/lib/lodge-deploy-backups/hub-20260806T225038Z-0eqEeE`，再迁移配置、binary 与 systemd unit；所有验收通过，未触发回滚。
- `config.json` 已从明文 `password` 原子迁移为 `passwordHash`；新 `session-secret`、config、SQLite/WAL/SHM 和 post-deploy 备份均为 `lodge:lodge 0600`。
- SQLite schema 2 完整性为 `ok`；3 台已配置主机首轮写入 3 条观测，第二轮增至 9，删除旧状态并重启后增至 12，3 台最新观测均在线。
- 旧 `state.json` 的摘要导入记录完成后，从 live 路径删除；其 owner-only 可恢复副本保留在回滚包内。post-deploy 一致性备份为 `/var/lib/lodge-hub/backups/post-deploy-20260806T225040Z-3787473.db`。
- Tailnet 会话 API 正常、受保护 API 未认证返回 401、Tailscale `AllowFunnel` 保持 0。`history_retention_days` 运行时指标由 0 更新为 30。

## [2026-08-07] deployment | 五台服务器全量纳管

- Hub 先部署安全 Agent upsert CLI，生成 root-only 回滚包和一致性 SQLite 备份；配置继续使用 Argon2id、`lodge:lodge 0600`，未认证 API 返回 401。
- tencent、banwagong 使用同一枚 Go 1.26.5、CGo-free x86-64 Agent，SHA-256 为 `35f22e2cc8cfd3a04ebc345e3d5402277c3b34972bb6d431a2000a3093bccd64`。
- 两台 Agent 均以 nologin `lodge` 用户运行，不在特权组，token 为 `0600`；真实服务进程 `NoNewPrivs=0`，固定 sudoers 无越权，服务上下文采集无 sudo 警告。
- Agent 使用 Tailnet-only TCP Serve `8443 → 127.0.0.1:9101`，Funnel 关闭；原有 tencent Caddy 与 banwagong Xray 公网监听保持运行。
- token 仅经加密 SSH 管道和 owner-only 暂存文件导入 Hub，未打印、未进入命令参数、本地磁盘、Git 或 SQLite；暂存文件已精确删除。
- Hub 最新观测为 5/5 在线、51 个 workloads、86 个 endpoints；41 个 workload 已归因，归因率 80.4%，尚未达到 95% 目标。
- banwagong 既有 `/etc/sudoers.d/hermes-ro` 为 0644 并被 sudo 忽略；Lodge 未修改或启用它，安装器验证没有增加新的 sudoers 诊断。
- 两个新节点当前 Tailscale `Tags` 为空；管理端点不是公网且仍有 bearer token，但 `tag:lodge-agent` 与负向 grants 需要在 Tailscale 管理面补验，不能宣称已完成最小权限 ACL。

## [2026-08-07] deployment | 五机 workload 归属升级

- 五台 Agent 滚动升级到 `0.2.0`、同一 CGo-free 静态二进制 SHA；每台先建 root-only 回滚点，再做服务上下文、越权、脱敏和 Hub 端验收。
- Docker systemd scope/host-network 监听归属与脱敏自定义进程来源上线；tencent 的 8 个 Node 端口归并成 4 个项目，bytebunny Nginx 和 banwagong cpa-manager-plus 归回容器。
- bytebunny、ali 从旧 HTTP Serve 迁移到 TCP Serve；旧连接池造成的短暂 404/offline 观测被保留，Hub 一致性备份并重启连接池后恢复。
- 最终 live SQLite 为 5/5 online、45 workloads、86 endpoints、0 unidentified、0 warnings，归因率 100.0%，完整性检查通过。
- 五机远程 staging 目录均已精确删除；每台回滚包和两台 Tailscale 路由备份保留。
