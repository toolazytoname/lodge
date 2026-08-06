---
type: synthesis
status: proposed
sources:
  - "[[2026-08-06-lodge-readonly-audit]]"
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

## 验收顺序

1. 管理页面只能通过 Tailnet 访问，公网 22 不再开放，密码 SSH 与 root SSH 均关闭。
2. 五台主机全部在线，能够稳定区分 workload、endpoint 与真实 Web 链接。
3. 能在测试机制造一次 SSH 失败突增/新监听端口，并在一分钟内收到去重告警。
4. 能从页面安全重启一个批准的服务，并留下不可抵赖的审计记录。
5. 最后才实现一次可回滚的声明式部署。
