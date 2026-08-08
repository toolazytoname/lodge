# Wiki Log

## [2026-08-08] deployment | M7 isolated stateless canary bootstrap

- 在 banwagong 通过 root console 对校验后的临时 helper 建立了唯一、精确的 `lodge-admin` sudo 许可；root-owned `0700` helper 运行后在成功或失败路径均删除该许可和自身。
- 引导写入 root-owned canary Compose 与部署策略，启动固定 Nginx baseline digest；`127.0.0.1:18080` HTTP 健康检查通过，未暴露公网端口、未挂载数据卷。
- 后续 Tailnet SSH 独立复核确认一次性 sudoers 文件与 helper 均不存在，`lodge-admin` 已恢复原本只读/SSH 维护白名单。
- M7 尚未完成：必须从已认证 Lodge Operations 读取实时 policy，执行 1.27.4 immutable release，再执行该 policy 的回滚并验证持久审计；不能绕开 Hub/Agent 受控链路。

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

## [2026-08-08] implementation | M5 SSH 攻击来源

- Agent 新增无参数 root helper，固定读取最近 10 分钟 OpenSSH journal，在 root 内聚合后只输出失败总量与 top 20 canonical source IP/count。
- SQLite schema 7 保存 privacy-minimized SSH summary；非法、过时、重复或越界 source 数据 fail closed，原始日志、用户名、成功登录和端口不入 Hub。
- Hub 新增 `ssh.bruteforce` 双阈值规则：30 total/10 per-source 开启，100/50 critical，10/3 解除；缺 telemetry 不误恢复。
- Security fixture 用保留测试 IP 验证攻击来源与次数，手机详情改为完整换行；事件规则量化达到 7/7。
- M5 实现项全部完成，五机 Agent 0.5.0、Hub 0.6.0/schema 7 的滚动发布与受控 live 证据仍待执行。

## [2026-08-08] production finding | 高日志量 SSH 采集收口

- `5e3a64d` 与视觉稳定化提交 `cd981b9` 的 push/PR CI 通过后，Hub 0.6.0 已事务发布到 bytedragon；schema 5→7、完整性、回滚包、发布后备份、5/5 旧 Agent 兼容、未认证 401 与 Tailnet-only 均通过。
- bytedragon、tencent、banwagong 的 Agent 0.5.0 逐机安装和 Hub 观测通过；bytebunny 在覆盖生产文件前被候选的五秒 journal 超时门禁阻止，服务和 sudoers 未改变。
- 只输出计时/字节数的诊断显示 bytebunny 即使查询最近 100 条 SSH journal 也需要约 16 秒；其固定 `auth.log` 最近十分钟约 155 KiB/1199 行。Agent 0.5.1 因此优先读取固定认证日志的 8 MiB 尾部，并以时间戳证明完整覆盖十分钟；无文件时才走五秒 journal 回退。
- 修正候选在 bytebunny 上 43 ms 完成，观测到 169 次失败、8 个来源，超过 critical 阈值；真实来源 IP 未进入开发日志、聊天摘要或 Git。新候选仍需完整 CI 和五机统一滚动发布。

## [2026-08-08] production | M5 历史与告警完成

- `2fc7825` 的 push/PR 双 CI 与漏洞扫描通过后，五台 Agent 全部逐机统一到 0.5.1 和同一静态二进制；每台验证 token 不变、真实服务 API、精确 sudo 允许、追加参数/直接 journal 拒绝、回滚点与 staging 清理。
- schema 7 全量终验为 5/5 online、55 workloads、86 endpoints、11 routes、0 warnings、0 unidentified，数据库完整性 `ok`；Hub 与四台远程 Agent 的 Serve 均为 Tailnet-only，同机 Agent 不发布 8443，未认证 Hub API 为 401。
- bytebunny 自然流量在部署开始后 27.9 秒形成 `critical/active` SSH 事件；同期 bytedragon 为 `warning/active`。终验窗口分别为 154/7 与 26/2（失败数/来源数），真实 IP 未写入 Git 或进度记录，事件也未被自动确认。
- 生产 Webhook 未配置，因此 outbox 为 0 且没有外发；适配器能力由自动化门禁验收。M5 完成，下一里程碑为 M6 受控运维动作。

## [2026-08-08] implementation | M6 Agent 受控动作边界

- Agent 0.6.0 移除 Docker prune、journal vacuum、直接 Caddy restart 三条旧写权限，只保留一个从有界 stdin 接收 action ID 的固定 root helper；Hub/HTTP 不能提交命令、参数或路径。
- root-only 0600 策略逐目标批准 systemd/Docker 的 start/stop/restart/logs；缺失即空清单，unsafe owner/mode/symlink、重复身份、路径/flag 注入和未知字段全部 fail closed。
- 状态操作有 15 秒总限时、前后状态与 10 秒健康验证；日志限 200 行/64 KiB，脱敏后只作瞬时响应，原始 stderr 与错误不越界、不入持久审计。
- 安装器把配置目录改为 `root:lodge 0750` 并移除服务写权限，阻断 `lodge` 通过 rename 替换 root 策略；真实安装将同时负向验证任意命令、旧写操作和动态 helper argv。
- Agent 切片门禁通过后才提交/推送；Hub 审计、确认 UI 与生产发布仍待后续切片，M6 保持进行中。

## [2026-08-08] implementation | M6 Hub 审计与 Operations 页面

- Hub 0.7.0 新增认证动作清单、CSRF 执行与有界审计 API；执行前重新读取 root policy，只接受 agent/action/逐字确认，不接受命令、参数、目标或 Agent 凭据。
- 非幂等 Agent POST 禁用 retry、redirect 和环境 proxy；全局串行动作以独立后台预算完成 `requested → running → succeeded|failed`，Hub 重启只标记 `hub_restarted`，不重放不确定动作。
- SQLite compare-and-set、分类错误、伪匿名会话身份及日志 secret sentinel 覆盖 DB/WAL/SHM；最近日志仅在当前认证响应中存在。
- Operations 页面完成实时权限、风险、Agent 同步、逐字确认、瞬时结果和持久审计，390/1280 视觉与行为测试通过；完整门禁、双 CI 和 production live 验收仍待完成。

## [2026-08-08] production | M6 受控运维完成

- `a650939` 首轮 CI 精确捕获 Linux Operations 整页比 macOS 少 5px；`4119a48` 只固定完整截图画布高度，不裁图、不放宽 4% 像素阈值，push/PR 双 CI、race、6 组 Chromium 与漏洞扫描全部通过。
- SHA-256 `6fc1a282...f12b041` 的 Agent 0.6.0 逐机滚动到五台主机；token 不变，root-only 策略动作数 3/3/0/3/13，任意命令、旧写权限和动态 helper argv 均被拒，四台远端 Serve 保持 Tailnet-only，同机 Agent 保持 loopback-only。
- SHA-256 `d68357c9...a94a44b` 的 Hub 0.7.0 事务上线，保留发布前回滚包和发布后一致性备份；schema 7、owner-only 文件、未认证 401、Tailnet-only Serve 与嵌入 Operations 资源全部通过。
- live Caddy log 因超过 64 KiB 安全失败且不重试；Caddy 幂等 start 成功并验证 running→running；Redis log 成功返回 200 行，实际样本不在 DB/WAL/SHM。三条审计为 2 succeeded/1 failed/0 interrupted，均使用伪匿名会话身份。
- 终验为 5/5 online、55 workloads、86 endpoints、11 routes、0 warnings、0 unidentified、最大观测年龄 24.6 秒。M6 完成，进入 M7 声明式部署。

## [2026-08-08] implementation | M7 Agent 声明式部署事务

- Agent 0.7.0 新增 root-only 声明式部署策略；缺失即空清单，版本 1 仅允许无状态单 Compose service 与 immutable sha256 release。
- root 预登记路径、service、health 和 release；Hub/HTTP 只能选择 ID，不能提交 Compose、环境、路径、镜像、命令或公网健康 URL。
- 首次变更捕获运行镜像 RepoDigest；canonical override 同时保存 current/previous，fsync 后原子提交，失败则重新应用旧镜像并验证健康。
- 量化安全控制达到 8/8；Hub 异步审计、Web 确认与 production 空策略滚动仍待完成，M7 保持进行中。

## [2026-08-08] implementation | M7 Hub 异步审计与 Web 发布

- Hub 0.8.0 对 list/execute 都重新读取 Agent root policy，只接受 agent/deployment ID 与逐字确认；调用者不能提交镜像、Compose、路径、环境、健康目标或命令。
- 发布与普通动作共用全局串行门禁；`requested/running` 在 HTTP 202 前落库，后台只发送一次有界 Agent POST，浏览器断开不取消，Hub 重启不重放。
- terminal 审计覆盖 succeeded、failed、rolled_back；自动恢复保留原失败分类，恢复失败明确为 rollback_failed。
- Operations 页面新增不可变发布区，展示当前/上一版本与缩短 sha256，精确确认后通过持久操作记录跟踪最终状态；390/1280 fixture 同时覆盖成功与已回滚呈现。
- Agent 安全控制 8/8、发布 terminal audit 3/3、Hub 边界回归 51/51、关键端到端场景 26；production 空策略滚动和首个经确认业务目标仍待执行，M7 保持进行中。
- 首轮 Linux CI 证明 Operations 结果页因平台字体度量比 macOS 短 6px；门禁继续比较完整页面，并把完整画布固定为 1765px，不裁图也不放宽像素阈值。
- 第二轮 Linux CI 在桌面通过后继续证明移动 Operations 页短 7px；移动完整画布固定为 2548px，使用相同的整页像素门禁。

## [2026-08-08] production | M7 平台 fail-closed 上线

- 最终 push/PR 双 CI、完整质量门禁和漏洞扫描通过后，五台 Agent 逐机滚动到同一 0.7.0 静态二进制并各自保留 root-only 回滚包。
- token 与动作策略摘要不变，真实服务 55、既有动作 22、发布权限 0；五台部署策略均不存在，状态目录 root:root 0700 且为空，任意命令和动态 helper 参数仍被拒。
- tencent 登录横幅破坏 scp，首次传输在写入前停止并清理空 staging；改用 root-only stdin 传输后通过。bytebunny 与 banwagong 的既有 sudoers 全局错误未被 Lodge 增加。
- Hub 0.8.0 事务上线，保留旧二进制/配置/一致性数据库回滚包并创建发布后备份；新 API 401、内嵌 UI、loopback、Tailnet-only Serve 与 credential scan 通过。
- 终验为 schema 7/integrity ok、5/5 online、55 workloads、86 endpoints、11 routes、0 warnings、0 unidentified、最大年龄 24.5 秒、0 in-flight。production 业务 deploy/rollback 保持 0，等待人工批准首个无状态 stack/digest。
- 上线后只读合格性审计发现现有 3 个 Compose service 中，PostgreSQL/Redis 明确有状态，new-api 具有可写 `/data` 与 `/app/logs` bind mount；M7 v1 合格目标为 0。需人工选择隔离 canary 或先重构业务状态边界。

## [2026-08-08] audit | M8 SSH 防爆破与访问收口基线

- 五台主机的只读有效配置均为 wildcard SSH 监听、`PasswordAuthentication yes`、`PermitRootLogin yes`、public-key enabled、`MaxAuthTries 6` 与 `LoginGraceTime 120`；因此 Tailnet 已运行不等于公网 SSH 已关闭。
- bytebunny、bytedragon、Ali 的 UFW inactive 且没有 active Fail2Ban；tencent 两者 active；banwagong 仅 UFW active。该证据不能推断云安全组或互联网实际可达性。
- 新增逐主机的 non-root key 管理员、独立 Tailnet 新会话、保留 console/recovery、`sshd -t`+reload、云边界和 firewall 回滚证据门禁。尚未修改任何 SSH、用户、防火墙、Fail2Ban、云规则或密码，M8 保持进行中。

## [2026-08-08] implementation | M8 实时 SSH 防护姿态

- Agent 0.8.0 新增无参数 root-only `--collect-security-posture`。它仅执行固定本机检查，在 root 内压缩为 SSH listener/password/root/public-key、UFW、Fail2Ban、Tailscale 七个 closed enum；不输出账户、密钥、规则、地址、原始输出、云侧状态或可达性结论。
- Hub 只在实时 host snapshot 暴露该姿态，避免重启后把历史 SSH 配置冒充当前结论；Security 页面独立呈现 5 台主机的风险/未知/离线状态，并明确不推断云安全组或公网实际可达性。
- 固定 helper argv、闭合词表、API 不泄露规则/命令、390/1280 安全页面和完整质量门禁均有回归验证。M8 仍待逐机管理员/recovery 证明与现场收口。

## [2026-08-08] rollout | M8 实时防护姿态上线

- 最终 push CI `31224459335` 与 PR CI `31224462704` 通过后，5 台 Agent `0.8.0` 与 Hub `0.9.0` 事务上线。五台服务均给出 7 个 closed-enum 字段，且额外 helper 参数均被 sudoers 拒绝。
- Hub 保持 loopback + Tailnet Serve；schema 7 integrity、5 configured hosts、14,154 observations、rollback bundle、post-deploy backup、未登录 API 401 与 recent log 不含已配置 secret 值均通过。Hub 进程所在地再次用既有凭据拉取 5/5 Agent，七个字段均有效且未输出 token/address/raw host data。此记录不改变 SSH/账号/防火墙/云边界，M8 仍待恢复路径和独立管理员验证。

## [2026-08-08] pilot | Ali key-only 管理员前置验证

- 经操作者确认 Ali 有 console/recovery 后，创建 locked-password `lodge-admin`，安装现有 Mac 公钥；home/`.ssh`/`authorized_keys` 为 owner-only。sudoers 经 `visudo` 验证，只精确允许 `sshd -t`、SSH service status/reload 与有界 journal，任意 sudo 已拒绝。
- 从原有入口取得的 ED25519 host fingerprint 与 Tailnet endpoint 一致后才写入本机 known_hosts。新 key Tailnet 登录抵达 Tailscale SSH 的额外交互认证门，后续成功完成 check-mode 验证。

## [2026-08-08] pilot | Ali SSH 访问收口完成

- 已用 `sshd -t` 后 reload 写入 root-owned drop-in：password、keyboard-interactive 与 root remote login 关闭；public-key 保留，`MaxAuthTries=3`、`LoginGraceTime=30`。从新 Tailnet `lodge-admin` 会话再次验证成功；公网 root 公钥和 password-only 连接均被拒绝。
- `lodge-admin` 是 locked-password 非 root 账号；其精确 SSH maintenance sudo 被允许，扩展参数与任意 sudo 均拒绝。Agent 当前 posture 已验证 password/root disabled、public-key enabled。云边界、UFW、Fail2Ban 和其他四台主机未在本次修改。

## [2026-08-08] pilot | bytebunny OpenSSH 收口与 Tailnet root 边界

- bytebunny 完成相同的 non-root key 管理员、精确 sudo、备份、Tailnet 新会话、`sshd -t`+reload 与公网拒绝验证。cloud-init 的 `50-cloud-init.conf` 先读入 password 设置，首个 `99-` drop-in 被安全发现未生效；保留备份后改为先于它的 `01-` drop-in，复验后 effective OpenSSH password/root/keyboard-interactive 都已关闭。
- 两台试点均发现 Tailnet SSH policy 可把 `root` 映射到本地 root；这独立于 OpenSSH `PermitRootLogin no`。因此暂停下一主机，等待 Tailnet policy deny-root/allow-lodge-admin 或显式关闭 Tailscale SSH 的选择。

## [2026-08-08] security | Tailnet SSH root 权限收口

- 经操作者授权，将唯一 Tailscale SSH rule 的 target user 从 `autogroup:nonroot, root` 改为精确 `lodge-admin`，保留 member-to-self 与 check mode。
- 新会话实测 Ali、bytebunny 的 Tailnet `root` 均被 policy 拒绝；两台 `lodge-admin` 均正常登录。至此两台试点的 OpenSSH 与 Tailnet SSH root 路径都已收口。

## [2026-08-08] rollout | bytedragon SSH 访问收口

- 通过原有 root 入口完成 backup、locked-password `lodge-admin`、Mac key 与精确 sudo；先验证新的 Tailnet 管理员会话和额外 sudo 参数拒绝。
- 以先于 `50-cloud-init.conf` 的 root-owned `01-lodge-hardening.conf` 通过 `sshd -t` 后 reload。effective OpenSSH 为 password/root/keyboard-interactive disabled、public-key enabled、3/30。
- 新 `lodge-admin` 正常；公网 root key 与 password-only 均被拒绝。Tailnet policy 已全网拒绝 root；云边界、UFW、Fail2Ban 仍待后续主机级收口。

## [2026-08-08] rollout | tencent SSH 访问收口

- 已有 UFW/Fail2Ban 保持不动。创建 backup、locked-password `lodge-admin`、Mac key 后先验证新 Tailnet 会话。
- 发现同名 sudo alias 的既有冲突；在任何 sshd 修改前改为无 alias 的精确命令白名单，完整 `visudo -c` 通过且额外参数仍被拒。
- `01-` drop-in 经 test+reload 生效；新管理员成功，公网 root key、password-only 与 Tailnet root 均被拒绝。仅剩 banwagong 的逐机访问收口。

## [2026-08-08] rollout | banwagong SSH 访问收口与 sudo 残余风险

- 最后一台完成 backup、locked-password `lodge-admin`、精确 sudo、新 Tailnet 会话、`01-` drop-in test+reload；effective OpenSSH 关闭 password/root/keyboard-interactive，保留 key 和 3/30。
- `lodge-admin` 成功；公网 root key、password-only、Tailnet root 均被拒，五台访问收口完成。
- 既有 `/etc/sudoers.d/hermes-ro` 权限错误使全局 `visudo -c` 报错。Lodge 不自动 chmod 这个非本项目文件，避免意外启用其既有权限；自身 0440 精确 sudo 已独立 parse 和实际验证。云边界、UFW/Fail2Ban 仍待独立门禁。

## [2026-08-08] cloud-edge | bytedragon 公网 SSH 关闭

- 操作者在已核验的唯一关联火山引擎 `Default` 安全组中，仅删除 `TCP 22 / 0.0.0.0/0` 入方向规则；不改业务端口、组间规则或 ICMP。
- 新公网 22 TCP probe 超时，新的 `lodge-admin` Tailnet 会话成功。`public_ssh_closed_hosts` 现为 1/5；其他主机云边界不作推断。

## [2026-08-08] cloud-edge | bytebunny 公网 SSH 关闭

- 操作者在独立火山引擎账号删除 bytebunny 的公网 TCP 22 入站规则；新 `115.191.29.26:22` probe 超时，而 `lodge-admin` Tailnet 登录仍成功。
- bytebunny、bytedragon 在两个独立账号均已验证关闭，`public_ssh_closed_hosts` 为 2/5；Ali、tencent、banwagong 仍需各自 provider 证据。

## [2026-08-08] cloud-edge | Ali 公网 SSH 与忘记的 gost 暴露关闭

- Ali 未保留单独 22，而有一条描述为 `gost 转发` 的 `所有流量 / 0.0.0.0/0`；它覆盖 SSH 与实际监听的 8388、10809。
- 操作者确认不再需要后删除这条唯一宽规则。新公网 probes 对 22、8388、10809 均超时，`lodge-admin` Tailnet 登录成功；公网 SSH 已关闭 3/5，且该 forgotten public gost path 被明确退役。

## [2026-08-08] cloud-edge | tencent 公网 SSH 关闭

- 操作者在腾讯云 Lighthouse 北京实例防火墙中删除公网 TCP 22 规则；未编辑业务规则。
- 新 `43.143.252.243:22` probe 超时，`lodge-admin` Tailnet 登录仍成功；8388、10809 亦未对公网开放。公网 SSH 已关闭 4/5，banwagong 仍需独立 provider 证据。

## [2026-08-08] documentation | M8 历史基线与当前状态校正

- 将最初的 SSH/UFW/Fail2Ban 基线明确标为历史快照，避免它与五机已完成的 key-only/non-root SSH 收口、4/5 已验证公网 22 关闭相互矛盾。
- 未把仍待 provider 证据的 banwagong 或本地 firewall/Fail2Ban 例外标记为完成。

## [2026-08-08] audit | M8 五机当前连通性复核

- 新公网 TCP 22 probes 表明 bytebunny、bytedragon、Ali、tencent 均超时，banwagong 仍可连通。
- 五台独立的 `lodge-admin` Tailnet 新会话全部成功；因此当前云边界计数仍是 4/5，banwagong 是唯一公网 SSH 例外，不能由 UFW 状态推断其 provider 规则。

## [2026-08-08] host-firewall | banwagong 公网 SSH 关闭

- 操作者通过 root provider console 在 UFW 先加入 `tailscale0` 的 SSH allow，再删除通用 TCP 22 allow；其他业务端口未改。
- 新 `67.216.196.122:22` Internet TCP probe 超时，而 `lodge-admin` Tailnet 登录仍成功。公网 SSH 已关闭 5/5；Fail2Ban inactive 与既有 `hermes-ro` sudoers 权限问题继续单独审查。

## [2026-08-08] audit | Fail2Ban 收口后处置

- 独立服务检查确认仅 tencent 的 `fail2ban-client` 已安装、active、enabled；bytebunny、bytedragon、Ali、banwagong 均未安装。
- 因五台公网 TCP 22 都已关闭，将其记录为有意的 defence-in-depth 选择，而非把每机安装 Fail2Ban 误当作安全完成条件。任何未来公网 SSH 例外必须重新明确 jail/log 兼容性；banwagong 的 `hermes-ro` 仍待内容审查。

## [2026-08-08] remediation | banwagong `hermes-ro` sudoers 隔离

- 内容审查确认该 mode-0644 文件实际为 `hermes-ro ALL=(ALL) NOPASSWD: ALL`，账号存在且 UID 1000；未观察到其进程、unit 或 cron 引用。这不是 read-only 权限。
- 操作者未 chmod 启用历史规则，而是将其移至 root-only `/root/lodge-quarantine`。随后 `visudo -c` 仅报告 intended sudoers parsed OK；Tailnet 再检确认 `/etc/sudoers.d/hermes-ro` 不存在且 `lodge-admin` sudo 无 policy warning。全局 sudoers 验证为 5/5。
