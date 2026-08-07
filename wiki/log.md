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
- Hub 增加旧路由 404 后的一次有界重连回归保护并事务部署；认证失败不重试，重复 404 仍失败，生产 binary、回滚包和部署后 SQLite 备份均校验通过。
- 最终 live SQLite 为 5/5 online、45 workloads、86 endpoints、0 unidentified、0 warnings，归因率 100.0%，完整性检查通过。
- 五机远程 staging 目录均已精确删除；每台回滚包和两台 Tailscale 路由备份保留。

## [2026-08-07] deployment | Compose 与完整 systemd 资产上线

- Hub 先升级到 SQLite schema 3，新增 Compose project/service 持久字段并保持 Agent 0.2.0 向后兼容；迁移前后数据库备份、权限和完整性均通过。
- Agent 0.3.0 发现 operator-managed active systemd 单元与全部 failed 单元；无监听服务也进入资产台账，vendor 单元仍默认折叠，已有监听归属不回退。
- Compose 只通过 root-owned Agent 的固定自调用读取 Docker 官方 project/service 标签；输出为校验后的三元组，不读取或返回完整标签、环境变量、命令行、工作目录或配置路径。
- 一版带复杂 Docker template 的 sudoers 在 tencent 真实服务上下文被安装器拒绝；主机原子恢复到 0.2.0。最终改为无动态参数的 root helper，五机逐台预检、安装、Hub 落库验收后发布完成。
- 最新 live SQLite 为 5/5 online、全部 Agent 0.3.0、55 workloads、86 endpoints、0 warnings、0 unidentified，最大观测年龄 28.7 秒；Compose 精确识别 banwagong `new-api` 的 3 个服务，并发现 4 个 failed systemd 单元。
- schema 3 `integrity_check=ok`；五枚 Agent token 在 SQLite、WAL、SHM 中均无命中；五机 staging 均已删除，逐机 root-only 回滚包保留。

## [2026-08-07] deployment | Caddy/Nginx 脱敏路由上线，M3 完成

- Hub 升级到 schema 4，规范化持久化脱敏代理路由；旧 Agent 继续兼容，主库、回滚库和部署后备份完整性均通过。
- Agent root helper 只输出校验后的 scheme/host/port/path 与无凭据上游 authority，不读取容器环境、不执行 `docker exec`，原始代理配置不离开 root 内存。
- banwagong 真实沙箱揭示 `nginx -T` 会被隐藏在 `/root` 的 TLS 材料阻断；没有放宽 Agent 沙箱，改为在 `/etc/nginx` 内按文件数、深度、类型和总大小上限安全展开 include，并覆盖软链接逃逸回归测试。
- 五台逐机升级到 Agent 0.4.1，同一静态二进制 SHA；每台保留 root-only 回滚点，服务 API、精确 sudo、越权拒绝、Hub 落库和 Tailnet/loopback 均验收后才继续下一台。
- banwagong 两次因验收脚本字段/格式失配触发自动回滚，均完整恢复原二进制和服务后再重试，真实验证了恢复链路；业务服务未改变。
- 最终 live SQLite 为 schema 4、5/5 online、55 workloads、86 endpoints、11 routes、0 warnings、0 unidentified，最大观测年龄 14.0 秒；3 个 Compose identity 和 4 个 failed unit 保持稳定。
- 数据库完整性为 `ok`，五枚 Agent token 在 SQLite/WAL/SHM 中无命中；五机 staging 已删除、回滚包保留。M3 完成，Web 链接主动可达率明确留给 M4 测量。

## [2026-08-07] implementation | M4 TypeScript Web 契约底座

- 用 live 脱敏 fixture 审计现有控制台，确认 5 台/55 行能渲染且无 console error，同时记录信息架构、筛选、溢出、风险可见性、prompt 编辑和状态覆盖缺口。
- 保留无框架、Go 内嵌、单二进制架构；新增 build-only TypeScript 6.0.3，将浏览器源码迁到 strict TypeScript 与原生 ES module。
- 从导出的 Go HTTP structs 生成 TypeScript declarations，Exposure/Kind 为闭合 union；生成声明与编译 JS 漂移会阻断 `npm test`。
- `/api/services` 删除重复的 raw status/services payload，改为紧凑 Agent 引用和 joined service views；注解 body 收窄为明确输入契约并有回归测试。
- CI 使用 Node 24 + `npm ci`，TypeScript 是唯一 devDependency，浏览器仍无第三方运行时依赖。M4 的类型契约项完成，产品页面与交互继续实施。

## [2026-08-07] implementation | M4 五页面产品壳与浏览器验收

- 建立 Overview、Hosts、Services、Security、Operations 信息架构；后两者明确标注 M5/M6 能力边界。
- 在 5 主机、55 服务脱敏 live fixture 上验收搜索、筛选、失败优先级、Web 快速入口与服务配置对话框。
- 1280 宽度无横向溢出、console error 为 0；390/1920 与异常状态自动化证据留给下一切片。

## [2026-08-08] implementation | M4 响应式与异常状态门禁

- 新增完全虚构的 5 主机/55 服务 fixture 和固定时间、locale、时区的 Chromium 矩阵，不把真实资产写入测试。
- 自动覆盖 390/1280/1920、empty、offline、partial failure、total error、搜索和安全注解输入，共 9 个关键场景。
- 提交 5 张视觉基线；`npm test` 与 CI 安装浏览器并阻断行为或截图漂移，失败时保留 7 天 trace/screenshot/diff。
- 全错误显示 `N/A` 而不是误导性的 0；只有成功返回空数组才表示没有资产。

## [2026-08-08] implementation | M4 Hub 视角 Web 入口检查

- 新增 SQLite schema 5，按 host/workload/URL 原子保存最新入口检查元数据，不保存响应内容、原始错误、解析地址或凭据。
- 主动检查仅由已登录操作者通过 CSRF 保护的 POST 触发；每轮最多 64 个 URL、8 并发，使用有界 `HEAD`、禁用 redirect 与环境 proxy。
- UI 区分未检查、Hub 可达、HTTP 5xx 与 Hub 不可达，并明确不把 Hub 视角等同于公网或浏览器视角。
- fixture 验证 CSRF 请求头与主动检查交互，关键端到端场景增至 10；三档宽度视觉基线已更新并人工检查。
- 实现已通过局部门禁，真实 Hub 部署和 live 可达率证据仍待事务发布后记录。

## [2026-08-08] deployment | M4 主动入口检查上线

- 提交 `1a8a698` 的 push/PR CI 与漏洞扫描通过后，事务发布 Hub `0.5.0` 静态二进制；SHA-256 为 `cc3ee4bf9c65b61d088a1d0eb4e3d096169920534010d96afdf92edaa22274fa`。
- 安装器保留 root-only 回滚包与 schema 4 一致性备份，迁移到 schema 5 后再创建发布后备份；数据库完整性、权限、5 台主机、Tailnet-only Serve、受保护 API 未认证 401 和新静态资源均通过。
- 发布后 fleet 保持 5/5 online、55 workloads、11 routes、0 warnings、0 unidentified，最大观测年龄 2.4 秒。
- 首次 Hub 检查的 16 个展示入口为 7 reachable、1 degraded、8 unreachable；其中 11 条注册代理路由为 6/1/4，可达率 54.5%，未达到 95% 目标。
- M4 产品能力完成；DNS、TLS、timeout、502 等真实资产问题保留为显式治理差距，没有删除失败证据来美化指标。

## [2026-08-08] implementation | M5 有界历史查询底座

- 新增 host-scoped `ObservationSummary` 与 `/api/history`，默认 120 点、上限 500 点，避免完整历史快照在浏览器边界被放大。
- 单条 SQLite 查询汇总在线状态、资源比例、workload/failed、公网绑定与 warning，保持新到旧顺序。
- 补齐领域、SQLite、MemStore 和 HTTP 边界测试；未知主机与非法 limit 明确失败。
- Web 时间线、事件生命周期、规则和通知仍在后续切片，M5 保持进行中。

## [2026-08-08] implementation | M5 Security 历史趋势

- Security 页面按需读取选中主机最近 120 个持久化摘要，不在首屏放大五台完整历史。
- 原生 SVG 展示在线率、归一化负载、内存和根磁盘；离线时缺失的资源点保持断线，不伪造成 0%。
- 当前暴露面与历史 API 解耦；history 503 只影响趋势区域，实时资产继续可用。
- 1280/390 响应式、主机切换和历史错误通过 Playwright，新增两张视觉基线，关键场景增至 13。

## [2026-08-08] implementation | M5 事件生命周期底座

- 建立 `active → acknowledged → resolved` 事件状态机；持续风险按 host-scoped dedupe key 原地更新，复发生成新事件并保留旧历史。
- Observation 与事件对账在同一 SQLite 事务提交，重复、跨主机、非法或倒序信号全部回滚。
- 新增 offline、内存、根磁盘、归一化 load、failed/unhealthy workload 与新增 wildcard listener 规则；资源使用双阈值抑制抖动。
- 首次/恢复 listener 建立基线，离线与部分采集保留无法证实恢复的既有风险；领域、规则、存储与 Hub 集成测试覆盖这些边界。
- 事件 API、Web 确认、SSH 规则、通知冷却和 webhook 仍待后续切片。

## [2026-08-08] implementation | M5 认证事件 API

- 新增认证 `GET /api/events`，支持全局/host 筛选并限制最多 500 条；响应省略内部 dedupe key。
- 新增 CSRF 保护的事件确认 POST；重复确认幂等，未知事件 404，已恢复事件 409。
- 事件 severity/state 由 Go 契约生成 TypeScript union；真实 SQLite API 测试覆盖认证、CSRF 和完整状态边界。
- 路线图“Observation history and event transitions”完成；Security UI、SSH 规则、cooldown 与 webhook 继续进行。

## [2026-08-08] implementation | M5 Security 事件中心

- Security 页面新增全局事件中心，默认聚焦进行中，可按主机和 active/acknowledged/resolved 生命周期筛选。
- 每条事件呈现严重度、状态、主机、类型、持续时间和最近观测；确认后仍保持进行中，直到观测恢复。
- 事件 API 独立 503 不会清空当前暴露面或历史趋势；1280/390 视觉基线经人工检查，无横向溢出。
- 关键端到端场景由 13 增至 17；量化差距为事件规则 6/7（缺 SSH）和通知适配器 0/1。

## [2026-08-08] implementation | M5 持久化 Webhook 通知

- 新增 SQLite schema 6 outbox；Observation、事件 transition 与通知投递原子提交，每个 event/transition/channel 唯一。
- worker 使用 30 秒可回收租约、稳定 `X-Lodge-Delivery` 幂等键和最多 8 次的有界退避，实现明确的 at-least-once 语义。
- 复发风险按上一次成功 open 做冷却；冷却期内先恢复的 pending open 被取消，不发送失去上下文的 recovery。
- Webhook 只接受 owner 配置的 HTTPS URL，禁 redirect/环境 proxy；可选 bearer 来自 0600 非符号链接文件。URL、secret、response 和 raw error 不入库、不入日志。
- notification adapter 从 0/1 提升到 1/1；SSH 来源规则仍是 M5 最后的功能缺口。

## [2026-08-08] quality | 事件 UI 跨平台门禁稳定化

- `95d7b7c` 的两套 GitHub CI 均暴露同一确定性问题：测试重试继承 fixture server 中已确认事件，且 Linux 的移动端页面比 macOS 少 15px。
- 每个 Playwright 场景现在先显式重置可变 fixture 状态；Security 移动画布固定为完整基线高度，仍比较整页而不是裁掉内容或放宽像素阈值。
- 本地完整 `npm test`（含 race、生成契约、五场景 Chromium 与七张视觉基线）重新通过；推送后的双 CI 继续作为最终证据。
