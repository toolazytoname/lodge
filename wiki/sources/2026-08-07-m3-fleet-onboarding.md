---
type: source
date: 2026-08-07
sources:
  - live read-only SSH and Tailscale verification
  - authenticated Agent API summaries without credential output
  - live Hub SQLite and owner-private config assertions
  - GitHub Actions quality runs
---

# 2026-08-07 M3 五台服务器全量纳管

## 交付边界

本次只完成 tencent、banwagong 的 Agent 安装、Tailnet 路由、Hub 安全注册和真实资产验收。没有开放任意远程命令，没有修改 SSH、防火墙、Caddy、Xray 或业务容器，也没有把 token 写入数据库或日志。

Hub 使用提交 `d6e8a9c` 新增的 `--upsert-agent`：token 只从非交互标准输入读取，配置先完整验证，再以 owner-only 临时文件、`fsync`、并发替换检查和原子 rename 更新。live Hub 二进制 SHA-256 为 `a53055e0bdf0bc9fa7db03bcc3b70188d48692dc13e962928505b7a8a1764cac`。事务升级创建回滚包 `/var/lib/lodge-deploy-backups/hub-20260806T230955Z-46Yw1B` 和 post-deploy 备份 `/var/lib/lodge-hub/backups/post-deploy-20260806T230957Z-3789728.db`；SQLite schema 2 完整性为 `ok`。

## Agent 发布物和安全不变量

- Go 1.26.5、`CGO_ENABLED=0`、Linux amd64 静态 Agent。
- SHA-256：`35f22e2cc8cfd3a04ebc345e3d5402277c3b34972bb6d431a2000a3093bccd64`。
- `lodge` 为 nologin 系统用户，不属于 `docker`、`sudo`、`wheel`、`adm`。
- Agent 与 Hub Go 进程只监听 loopback；Tailscale 使用 raw TCP Serve，避免依赖 Hub 的 MagicDNS/HTTP Host 路由。
- Agent token 和 Hub config 均为 `lodge:lodge 0600`。
- sudoers 由 Agent 固定 argv 生成，不使用全局 Alias；候选策略和全机策略增量均验证，`docker run` 越权测试失败是预期结果。
- 安装器直接检查 `/proc/MainPID/status` 的 `NoNewPrivs=0`，并通过真实 systemd Agent API 要求服务发现非空且无 sudo/no-new-privileges 警告。

## tencent

- Debian 12 x86-64，Tailscale IPv4 `100.71.151.6`。
- 最终路由：Tailnet-only TCP `8443 → 127.0.0.1:9101`；Funnel false。
- Tailscale 变更前证据：`/var/lib/lodge/tailscale-backups/20260806T233724Z-3836051`。
- 既有 Caddy 容器和公网 8443 行为未改变。
- 服务上下文采集：16 services、0 warnings；Hub 最新投影为 16 workloads、21 endpoints，其中 8 个裸监听 workload 尚未归因。

初版 unit 同时使用 sudo 白名单和会隐式触发 `NoNewPrivs=1` 的 systemd 沙箱，导致在线但资产为空。真实 `/proc` 与服务 API 验收发现该问题；修复后保留非 root 用户、`ProtectSystem=strict`、`ProtectHome`、`PrivateTmp` 和 `ProtectControlGroups`，移除与 setuid sudo 边界冲突的 seccomp/capability 沙箱项。

## banwagong

- Debian 13 x86-64，Tailscale IPv4 `100.93.74.76`。
- 最终路由：Tailnet-only TCP `8443 → 127.0.0.1:9101`；Funnel false。
- Tailscale 变更前证据：`/var/lib/lodge/tailscale-backups/20260806T235507Z-3032620`。
- 既有 Xray 公网 8443 监听保持运行。
- 服务上下文采集：12 services、0 warnings；Hub 最新投影为 12 workloads、14 endpoints，其中 1 个尚未归因。

主机在 Lodge 安装前已有 `/etc/sudoers.d/hermes-ro` mode 0644，`visudo -c` 报 bad permissions，sudo 当前忽略该文件。直接改为 0440 可能意外启用旧授权，因此 Lodge 未修改它。安装器保存并比较过滤后的诊断基线，只接受“既有错误完全不变且 Lodge 候选独立合法”的结果；任何新增错误均恢复安装前 Lodge 策略。

## 全 fleet 验收

| Host | Online | Workloads | Endpoints | Unidentified |
| --- | ---: | ---: | ---: | ---: |
| bytedragon | 1 | 8 | 19 | 0 |
| bytebunny | 1 | 10 | 21 | 1 |
| ali | 1 | 5 | 11 | 0 |
| tencent | 1 | 16 | 21 | 8 |
| banwagong | 1 | 12 | 14 | 1 |
| **Total** | **5/5** | **51** | **86** | **10** |

已归因 workload 为 41/51，即 80.4%。5/5 纳管目标完成，但 95% 归因门禁未通过；后续必须增加 Compose 标签、完整 systemd/failed-unit、自定义进程和 Caddy/Nginx 脱敏路由发现。

在验收时间点，五台最新观测最大年龄为 14.0 秒。Hub config 仍为 `lodge:lodge 0600`，包含 `passwordHash` 而不含明文 `password`；未认证 `/api/agents` 返回 401。tencent、banwagong token 导入暂存文件和远程部署目录均已删除。

> [!note]
> tencent 与 banwagong 的 Tailscale `Tags` 当前为空。Serve/Funnel 验证证明管理端点不在公网，Agent bearer token 仍提供第二道边界；但 `tag:lodge-agent` 和“普通 tailnet 节点不能访问 8443”的负向 grants 尚未在管理控制台验收，因此最小权限 Tailnet ACL 仍是明确待办。

## CI 证据

- `d6e8a9c`：[quality 31129778895](https://github.com/toolazytoname/lodge/actions/runs/31129778895)
- `0a26321`：[quality 31130733131](https://github.com/toolazytoname/lodge/actions/runs/31130733131)
- `5cfa1f2`：[quality 31131263036](https://github.com/toolazytoname/lodge/actions/runs/31131263036)
- `21eb0b4`：[quality 31131614944](https://github.com/toolazytoname/lodge/actions/runs/31131614944)
- `0d77509`：[quality 31132153479](https://github.com/toolazytoname/lodge/actions/runs/31132153479)
- `cd165b7`：[quality 31132585365](https://github.com/toolazytoname/lodge/actions/runs/31132585365)

所有相关提交均通过全量质量门禁、race detector 和 `govulncheck`；用于隔离 GitHub 延迟并发队列的临时 CI tags 在验收后均已删除。
