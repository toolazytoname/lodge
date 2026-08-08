---
type: synthesis
status: active
sources:
  - "[[2026-08-06-lodge-readonly-audit]]"
  - "[[2026-08-07-m3-fleet-onboarding]]"
  - "[[2026-08-07-m4-web-console]]"
---

# Lodge 服务器管理台实施路线

## 产品边界

Lodge 应成为“资产真相 + 风险感知 + 受控操作”的私人运维控制台，而不是浏览器里的任意 root Shell。

核心对象分为四层：

1. **Host**：机器身份、系统资源、补丁状态、Tailscale 状态和安全基线。
2. **Workload**：Docker/Compose、systemd、自定义进程及其来源与依赖。
3. **Endpoint**：监听地址、端口、协议、域名、反向代理路径和真实可达性。
4. **Operation**：经过策略批准的启动、停止、重启、更新、回滚和清理，全部留审计记录。

## Phase 0：先收口攻击面

- 将 Hub 从 Tailscale Funnel 切换为 tailnet-only Serve；管理面默认不公开。
- 每台服务器先创建并验证非 root 管理账号，再禁用 SSH 密码和 root 远程登录；变更前保留云厂商控制台/救援模式作为回退。
- 公网安全组移除 22，或只允许固定来源；日常 SSH 走 Tailscale IP/MagicDNS。
- 同一个密码全部作废：每台机器保留不同的高熵 break-glass 密码，存进成熟密码管理器；日常不使用密码 SSH。
- 给服务器使用 Tailscale tag；用 grants 只允许：个人设备访问 Hub/SSH，Hub 访问 agent 端口，其他节点不能访问 agent。
- 如采用 Tailscale SSH，对高风险账号使用 `check` 重新认证；否则继续使用传统 OpenSSH over Tailscale。

## Phase 1：把看板做成可靠资产台账

- 给 tencent、banwagong 安装 agent，Hub 纳管全部 5 台唯一主机；tencent/dev 是同一 Host 的一个登录身份，不建第二台机器。
- 将 `Service` 拆为 workload 与 endpoint，避免把 SSH、Tailscale 端口、业务 Web 页面混为一个概念。
- 发现源扩展为 Docker 容器、Compose project、用户自建 systemd unit、failed unit、PM2/常驻进程；系统基础服务默认折叠。
- 解析 Caddy/Nginx 的只读、脱敏路由信息，得到真实域名；人工注解只负责纠错与补充。
- 暴露状态改为：`local`、`tailnet`、`bound-public`、`confirmed-public`。Hub 从外部探测确认后才能标记 `confirmed-public`。
- 使用 SQLite 保存主机、快照、事件、注解和操作审计；给 schema 做版本迁移和备份。

> [!note]
> 2026-08-07 已完成领域契约、SQLite schema 4、顺序迁移、Hub 接线、30 天观测裁剪和一致性备份，并通过事务安装器部署到 live Hub。五台 Agent 已统一升级到 `0.4.1`：Docker systemd scope、脱敏自定义进程、Compose project/service、operator-managed systemd、failed unit 与 Caddy/Nginx 脱敏路由发现均上线。当前 5/5 在线、55/55 workload 已归因（100.0%），共 11 条代理路由、0 warnings、0 unidentified；M3 完成。Web 链接真实可达率仍按 M4 的主动探测门禁验收，不把“已发现”冒充“已可达”。

## Phase 2：让异常主动来找人

- agent 新增安全信号：SSH 成功/失败计数、来源摘要、密码登录事件、Fail2Ban 状态、firewall 状态、failed units、待更新数量、重启需求。
- Hub 做状态差异与规则引擎，优先实现：新公网端口、SSH 失败突增、密码登录成功、新服务出现、主机离线、磁盘/内存/负载阈值、服务失败。
- 告警需要去重、恢复通知、冷却时间和确认状态，避免每 30 秒刷屏。
- 通知先做一个简单可靠的 webhook，再按需要接 Telegram/飞书/邮件/ntfy。
- 首页增加“安全中心”和时间线，让用户能回答：什么时候开始异常、来自哪里、采取了什么动作、是否恢复。

## Phase 3：接通受控管理动作

- Hub 新增动作代理 API，但永不接受 shell 文本；请求只能是结构化动作，例如 `restart workload X`。
- agent 只允许命中 root 所有、不可由 lodge 修改的策略文件中的目标。动态 systemd/container 操作由窄权限 root helper 校验，不给 `lodge` docker 组，也不开放 `systemctl *`/`docker *` sudo 通配。
- 首批动作：重启/停止/启动已批准 workload、查看最近日志、Docker 安全清理、重启后健康检查。
- 每次操作记录操作者身份、主机、目标、前后状态、时间、输出摘要和结果；危险动作要求二次确认。
- 加 CSRF 防护、严格 URL/字段校验、安全响应头、独立 session secret，并将 Hub 密码改为 Argon2id/bcrypt 哈希；优先使用 Tailscale Serve 身份头完成单用户认证。

## Phase 4：部署与更新

- 把“部署”建模为声明式 stack，而不是网页终端：镜像/Compose 文件版本、环境、健康检查、备份、回滚点。
- 最小闭环：预检 → 拉取固定 digest → 启动 → 健康检查 → 成功登记；失败自动回滚。
- secret 不进入 Hub 日志和普通数据库；可选接 SOPS/age 或成熟秘密管理器。README 中的 vault 在完成威胁模型前不作为安全承诺。
- 所有更新先支持单机、单服务；批量滚动升级放在验证稳定之后。

## 推荐页面

- **Overview**：五台主机、健康、风险数、最近变化。
- **Hosts**：资源趋势、workloads、endpoints、SSH/防火墙/Tailscale 基线。
- **Services**：服务目录、所属主机、域名、暴露范围、快速打开 Web。
- **Security**：登录事件、攻击来源摘要、新暴露、基线偏差、待办修复。
- **Operations**：允许的动作、部署记录、回滚与完整审计日志。

> 2026-08-07 M4 已完成五页面产品壳的首个可用切片。Overview、Hosts、Services 使用 5 主机/55 服务脱敏 live 数据；Security 与 Operations 只显示当前具备的数据，并分别明确标注 M5 历史告警和 M6 受控动作边界。服务搜索、主机/暴露/状态筛选、失败优先级、Web 快速入口和可访问注解对话框已在 1280 宽度验收，0 console error、0 横向溢出。390/1920 自动视觉回归和异常状态 fixture 尚未完成，M4 不视为整体完成。

> 2026-08-08 M4 的响应式与异常状态门禁完成：虚构 5 主机/55 服务 fixture 固定时间和环境，在 390/1280/1920 验证 9 个关键场景并提交 5 张截图基线；empty、offline、partial 与 total error 不再混淆。相关 `npm test` 门禁完成后，M4 剩余的核心证据是注册 Web 链接主动可达率，不能仅以“发现 URL”替代。

> 2026-08-08 M4 主动检查实现完成并进入发布验收：Hub 只在操作者显式触发后，从自身网络视角对当前展示 URL 做有界、无正文的 `HEAD`，SQLite schema 5 原子保存最新元数据。UI 不再把发现 URL 冒充可达，并明确区分 100–499、5xx 与网络失败。关键端到端场景增至 10；生产部署和 live 测量完成前，M4 仍保持进行中。

> 2026-08-08 Hub `0.5.0` 与 schema 5 已事务发布，M4 完成。首次真实测量中，11 条注册代理路由只有 6 reachable、1 degraded、4 unreachable，可达率 54.5%；全部 16 个展示入口为 7/1/8。这个结果低于 95% 运行目标，但证明产品会暴露 DNS、TLS、timeout 和 502，而不是把“发现”包装成“健康”。资产治理继续保持显式红项，不阻塞 M5 历史与告警能力开发。

> 2026-08-08 M5 已完成有界趋势、持久事件状态机、认证事件 API、Security 事件中心与可靠 Webhook。schema 6 outbox 将事件 transition 和投递原子提交，以租约、稳定幂等键、最多 8 次退避、复发冷却和陈旧 open 取消提供 at-least-once 语义；notification adapter 达到 1/1。M5 仍缺 SSH 攻击来源聚合规则与 production live 验收，不能提前宣称“能看到谁在爆破”。

> 2026-08-08 M5 的 SSH 代码项完成：Agent root helper 只把 10 分钟窗口、失败总量和 top 20 canonical source IP/count 带出 root，Hub schema 7 持久化并以 30 total/10 per-source 开启、100/50 critical、10/3 解除的迟滞规则生成 `ssh.bruteforce` 事件。缺失 telemetry 不误恢复，Web 手机端也完整展示来源。规则 7/7、通知 1/1；但 IP 是网络来源而非人员身份，且五机滚动发布/live ≤90 秒证据完成前 M5 仍处于验收阶段。

> 2026-08-08 首轮生产滚动揭示 bytebunny 的 journal 即使只取最近 100 条也约需 16 秒，0.5.0 的五秒预检在任何覆盖前安全停止。0.5.1 改为固定 auth.log/secure 的 8 MiB 有界尾部并证明覆盖完整十分钟，无文件才回退 journal；真实候选在 43 ms 内看到 169 次失败、8 个来源，来源 IP 未写入 Git 或进度记录。Hub 0.6.0/schema 7 和三台 0.5.0 已健康，统一升级与事件延迟验收仍待完成。

> 2026-08-08 M5 完成：双 CI 通过后，Hub 0.6.0/schema 7 与五台 Agent 0.5.1 统一发布，终验 5/5 online、55 workloads、86 endpoints、11 routes、0 warnings、0 unidentified。bytebunny 自然 critical SSH 信号从部署开始到持久化为 27.9 秒，bytedragon 同期 warning；真实来源只在认证事件页中可见且未自动确认。Webhook 未配置所以没有生产外发。下一步进入 M6 受控动作，不能把已有 Agent 只读 sudo 白名单误宣称为 Web 运维能力。

> 2026-08-08 M6 Agent 权限边界实现：旧 Docker prune、journal vacuum、直接 Caddy restart 写规则已移除，改为 root-only 策略 + 单一固定执行 helper。策略缺失即禁用全部动作；`lodge` 不可写策略父目录；HTTP 只处理动作 ID 和类型化有界结果。start/stop/restart/logs 的固定映射、串行锁、超时、状态验证与日志脱敏已有测试，但 Hub CSRF 代理、确认短语、持久审计、Web 页面和五机 live 验收尚未完成，不能从页面执行任何动作。

> 2026-08-08 M6 Hub/Web 闭环实现：Hub 0.7.0 每次执行前重新读取 Agent 实时策略，只接受 agent ID、action ID 与逐字确认语；非幂等 POST 不走 proxy/redirect/retry。operation 以 compare-and-set 持久化请求、运行和分类结果，重启中断只标记 `hub_restarted` 而不重放，瞬时日志由 sentinel 测试证明不进入 DB/WAL/SHM。Operations 页面在 390/1280 完成权限、风险、确认、结果与审计验收；完整 CI 和生产低风险动作完成前 M6 仍保持进行中。

> 2026-08-08 M6 完成：跨平台 5px 视觉差异经固定完整画布而非降低阈值修复后，双 CI 与漏洞扫描通过。五台 Agent 0.6.0 和 Hub 0.7.0 事务发布；live Caddy 日志超限按 `log_read_failed` 安全失败且不重试，幂等 start 保持 running→running，Redis 200 行瞬时日志成功且样本不在 DB/WAL/SHM。终验 5/5、55 workloads、86 endpoints、11 routes、0 warnings、3 条 terminal audit、0 interrupted。下一步只做声明式、可预检和可回滚的 M7，不把 Operations 扩展成 Web Shell。

> 2026-08-08 M7 代码闭环完成：Agent 0.7.0 以 root-only policy、stateless-only、immutable digest、local health、canonical current/previous state 和独立恢复预算执行单服务事务。Hub 0.8.0 每次重新列出权限，在 HTTP 202 前持久化 running，与普通动作共享串行门禁，再异步发送一次不重试请求；Web 只显示 release identity/摘要并轮询 succeeded、failed、rolled_back。自动化达到安全控制 8/8、terminal audit 3/3、Hub 边界回归 51/51 和关键端到端 26。生产先以空策略 fail closed 滚动；首个真实 stack/digest 必须另行审查，不能由资产发现自动推断。

> 2026-08-08 M7 平台已 fail-closed 上线：最终双 CI 与漏洞扫描通过后，五台 Agent 0.7.0 和 Hub 0.8.0 事务滚动完成。55 workloads、86 endpoints、11 routes、0 warnings、0 unidentified 保持，既有动作 22，发布权限 0；策略不存在、状态为空、token 未进 SQLite/WAL/SHM，管理面继续 Tailnet-only。production 业务发布仍是 0 次，只有操作者明确批准一个无状态 stack、固定 digest、health 与 recovery plan 后才完成最后 live rollback 验收。

> 2026-08-08 M8 首个证据切片：重新只读检查五台主机有效 SSH 配置，全部仍是 wildcard `:22`、密码认证开启、root 远程登录开启；bytebunny、bytedragon、Ali 还没有 active UFW/Fail2Ban 双层防护。Tailnet 五机均 running，但不等于公网 SSH 已关闭。仓库已固化每台独立恢复路径、非 root key 管理员、Tailnet 新会话、`sshd -t`+reload、云边界回滚的收口门禁；尚未做会导致锁死的批量变更。下一步必须先确认每台的 console/recovery 和可用管理员 key，再逐机执行。

> 2026-08-08 M8 实时可见性切片：Agent 0.8.0 通过第五个无参数 root-only helper 把 effective SSH listener/password/root/public-key、UFW、Fail2Ban、Tailscale 压缩为七项 closed enum；不带出用户、密钥、规则、地址、命令输出或云安全组。Hub 只把它作为当前 snapshot，Security 页面明确显示未知/未安装而不把它们涂绿；桌面与手机视觉验收通过。现场 SSH 收口仍等待逐机 recovery 和独立管理员 key 验证。

> 2026-08-08 M8 可见性已上线：最后双 CI 后，五台 Agent 0.8.0 和 Hub 0.9.0 完成事务升级。逐机验证服务态、7 项 closed-enum posture 与拒绝额外 helper 参数；Hub 验证 schema 7、5 configured hosts、14,154 observations、备份/rollback、未登录 401、loopback + Tailnet Serve 和日志不含已配置 secret 值。它使风险立即可见，但并不替代逐机 recovery/admin key 门禁，也没有收口任何 SSH 入口。

> 2026-08-08 M8 Ali 试点完成：在已确认 console/recovery、Mac key、Tailnet check-mode 和新非 root 会话后，root-owned drop-in 经 `sshd -t`+reload 生效：password/root/keyboard-interactive 均关闭，public-key 保留，尝试和宽限期收紧。新 Tailnet `lodge-admin` 成功，公网 root key 与 password-only 均被拒绝；Agent posture 同步为 password/root disabled。云端 22、UFW/Fail2Ban 与其余四机仍保持待逐机验收，不能由本试点外推。

> 2026-08-08 M8 bytebunny 试点完成 OpenSSH 收口：cloud-init 文件排序导致首个晚编号 drop-in 未覆盖 password auth，已在不锁机的情况下保留备份、前置 Lodge drop-in 并复验有效配置。Ali 与 bytebunny 的 OpenSSH password/root/keyboard-interactive 现已关闭。

> 2026-08-08 M8 Tailnet SSH root 边界已收口：经操作者批准，唯一规则从 `autogroup:nonroot, root` 变为精确 `lodge-admin`，member-to-self check mode 保留。Ali 与 bytebunny 新会话均证明 Tailnet `root` 被拒绝，而 `lodge-admin` 可用；因此两台试点不再有独立 Tailnet root 绕过路径。

> 2026-08-08 M8 bytedragon 完成：其 cloud-init 同样要求 Lodge drop-in 位于 `50-` 之前；独立管理员先验证、`sshd -t`+reload 后，OpenSSH password/root/keyboard-interactive 已关闭且公钥保留。Tailnet `lodge-admin` 可用，公网 root-key 和密码尝试均被拒绝。Ali、bytebunny、bytedragon 现为三台完整访问收口证据，剩余 tencent 与 banwagong 仍按独立 recovery gate 推进。

> 2026-08-08 M8 tencent 完成：保留既有 UFW/Fail2Ban；创建管理员后发现 sudo 命令 alias 与既有环境冲突，先改为无 alias 的精确 whitelist 并全局验证，再配置 sshd。新管理员可用，OpenSSH root/password/keyboard-interactive 关闭，公网 root-key、密码和 Tailnet root 全部拒绝。仅剩 banwagong。

> 2026-08-08 M8 五机访问收口完成：banwagong 同样完成 non-root 管理员、`01-` profile、test+reload 与四类连接验证，至此五台都关闭 password/OpenSSH-root/Tailnet-root，并保留可用 `lodge-admin`。banwagong 的既有 `hermes-ro` sudoers 权限错误被明确记录但未擅改，因修复会激活非 Lodge 的历史权限。M8 余项是云端 22、firewall/Fail2Ban 和该既有 sudo 文件的独立审查，不应混入已完成的访问收口。

> 2026-08-08 M8 首个云边界验收：bytedragon 的唯一关联火山引擎安全组已只删除公网 `TCP 22 / 0.0.0.0/0`。从外网的新连接超时、Tailnet `lodge-admin` 成功，量化为公网 SSH 已关闭 1/5；其余主机仍逐台处理，业务端口和其他规则未动。

> 2026-08-08 M8 bytebunny 云边界验收：在其独立火山引擎账号完成同样的公网 TCP 22 关闭，外网超时、Tailnet 管理成功。两台不共享账号，因而是两份独立证据；公网 SSH 已关闭 2/5。

> 2026-08-08 M8 Ali 云边界验收：发现其公网 SSH 被描述为 `gost 转发` 的 all-traffic 宽规则覆盖，且 8388、10809 同时暴露。操作者确认服务已遗忘并删除宽规则；三端口外网均超时、Tailnet 管理正常。此举显式退役公网 gost，公网 SSH 已关闭 3/5。

## 验收顺序

1. 管理页面只能通过 Tailnet 访问，公网 22 不再开放，密码 SSH 与 root SSH 均关闭。
2. 五台主机全部在线，能够稳定区分 workload、endpoint 与真实 Web 链接。
3. 能在测试机制造一次 SSH 失败突增/新监听端口，并在一分钟内收到去重告警。
4. 能从页面安全重启一个批准的服务，并留下不可抵赖的审计记录。
5. 最后才实现一次可回滚的声明式部署。
