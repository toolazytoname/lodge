---
type: source
date: 2026-08-06
sources:
  - local Lodge repository
  - read-only SSH inspection of managed servers
---

# 2026-08-06 Lodge 只读审计

> [!note]
> 本页记录一次时间点快照，不是完整入侵取证。SSH 失败表示互联网扫描或爆破尝试，不等于攻击者已经登录；未发现异常也不能排除更早事件、日志缺失或日志被清理。

## 当前实现

- Go 单二进制 `hub + agent` 架构，现有测试全部通过。
- agent 以 `lodge` 非 root 用户运行，监听 `127.0.0.1:9101`，通过固定 argv 和 sudoers 精确白名单采集或执行少量动作。
- hub 每 30 秒拉取 agent，可展示在线状态、负载、内存、磁盘、Docker、监听端口及推测的暴露范围。
- 当前 Hub 配置了 3 台 agent：bytedragon、bytebunny、Ali，检查时均在线。
- tencent、banwagong 尚未部署 agent。
- agent 已实现 `docker-prune`、`journalctl-vacuum`、`restart-caddy` 三个固定动作，但 hub API 和 Web UI 尚未接通动作链路。
- README 声称存在浏览器端加密 vault，当前 hub API、存储和内嵌前端中没有对应实现。
- 当前观测数据仅在内存中保留；JSON 只保存 agent 配置和人工注解，没有历史、事件或告警。

## 当前部署拓扑

- bytedragon 运行 Lodge hub 与 agent。
- bytebunny、Ali 运行 Lodge agent，并通过 tailnet-only 的 Tailscale Serve 暴露 agent。
- 审计时 bytedragon 的 Hub 通过 Tailscale Funnel 公开在互联网；此状态已在下方的 M1 修复记录中收口。
- Hub 登录密码已设置；agent token 和 Hub 密码仍以明文保存在 root 可读配置中。

## Lodge 当前看到的资产

| 主机 | Lodge 状态 | 观测服务 | 标为 public | 摘要 |
| --- | ---: | ---: | ---: | --- |
| bytedragon | online | 8 | 2 | Caddy、SSH、Mihomo、Lodge hub/agent、Tailscale |
| bytebunny | online | 10 | 4 | Nginx、Happy、Clash、CUPS、8765 Python/OpenClaw 相关进程、SSH |
| Ali | online | 5 | 2 | Mihomo、SSH、Lodge agent、Tailscale |
| tencent | 未接入 | — | — | Happy/Caddy 容器、OpenCode、Node 服务、Xray、Fail2Ban |
| banwagong | 未接入 | — | — | Nginx、new-api、Postgres、Redis、CPA Manager、CLI Proxy API、Xray |

`public` 当前只表示套接字绑定到 `0.0.0.0`、`::` 或 `*`，没有结合云安全组、主机防火墙或外部探测，因此应理解为“潜在公网暴露”。

## SSH 与防护基线

可读取有效 sshd 配置的机器均为：

- `PasswordAuthentication yes`
- `PubkeyAuthentication yes`
- `PermitRootLogin yes`
- `MaxAuthTries 6`

防护状态：

- tencent：UFW active，Fail2Ban active。
- banwagong：UFW active，Fail2Ban inactive。
- bytebunny、bytedragon：UFW 服务单元 active，但 `ufw status` 为 inactive；Fail2Ban inactive。
- Ali：UFW inactive，Fail2Ban inactive。

最近 7 天失败认证计数（日志口径，可能受保留策略影响）：

- bytedragon：约 204,111。
- banwagong：约 28,766。
- tencent：约 388。
- Ali：约 87。
- bytebunny：日志量较大，限时查询未完成，未记录不可靠数字。

最近 30 天查询未发现 `Accepted password`。看到的成功会话均为公钥认证；多数来自当前本地公网地址，banwagong 还接受过来自 tencent 公网地址的另一把 ED25519 公钥。需要由所有者确认这把跨服务器密钥是否为预期自动化。

## 需要修正的实现风险

1. Hub 管理面当前经 Funnel 公网暴露，攻击面不必要。
2. Hub 密码明文存储，且会话 HMAC 密钥直接由登录密码派生；应改为慢哈希密码验证与独立随机会话密钥。
3. 会话 Cookie 未设置 `Secure`，也没有显式 CSRF token。
4. 人工 URL 未限制为 `http/https`；前端把服务字段拼进内联 `onclick`，HTML 转义未覆盖单引号，存在注入风险。
5. 注解 API 未验证 agent ID、service key 是否真实存在，也未限制 body 大小和字段长度。
6. `public` 是绑定地址启发式，不是真实可达性结论。
7. systemd 发现只覆盖有监听端口的单元；`user@0.service` 会把多个用户进程合并，无法准确回答“具体跑着什么”。
8. 常见端口自动猜 URL 会给 Tailscale agent 等基础设施生成误导链接，反向代理域名也无法从端口推断。
9. agent 固定动作尚未被 Hub 转发；未来不能简单开放任意命令或通配 sudoers。

## 2026-08-07 M1 修复记录

- 在保留独立 SSH 恢复连接的前提下，将 bytedragon 的 Hub 10000 从
  Tailscale Funnel 切换为 tailnet-only HTTPS Serve。
- 变更前状态保存于服务器的 owner-only
  `/var/lib/lodge/tailscale-backups/20260806T213623Z-3779055/`。
- 变更后 `AllowFunnel` 条目数为 0，Serve 精确代理
  `http://127.0.0.1:9102`，Hub 与 Agent 仍只监听 loopback。
- 本机 Tailscale 从 Stopped 恢复为 Running 后，可通过 Tailnet 请求
  `/api/session` 并得到 `{"authed":false}`；公网 Funnel 未恢复。
- 该记录证明公网管理端点由 1 降为 0；细粒度 grants 仍需按
  `docs/tailnet-deployment.md` 在管理控制台验证。

## 外部参考

- [Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve)
- [Tailscale Funnel](https://tailscale.com/kb/1223/funnel)
- [Tailscale grants](https://tailscale.com/docs/features/access-control/grants)
- [Tailscale SSH](https://tailscale.com/docs/features/tailscale-ssh)
- [OpenSSH sshd_config](https://man.openbsd.org/sshd_config)
