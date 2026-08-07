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

本页记录三个连续交付：先将 tencent、banwagong 安全纳入 Hub，再把 Docker systemd scope 和脱敏自定义进程归属能力滚动发布到五台 Agent，最后上线 Compose 与完整 systemd/failed-unit 发现。全程没有开放任意远程命令，没有修改 SSH、防火墙、Caddy、Xray 或业务容器，也没有把 token 写入数据库或日志。

Hub 使用提交 `d6e8a9c` 新增的 `--upsert-agent`：token 只从非交互标准输入读取，配置先完整验证，再以 owner-only 临时文件、`fsync`、并发替换检查和原子 rename 更新。live Hub 二进制 SHA-256 为 `a53055e0bdf0bc9fa7db03bcc3b70188d48692dc13e962928505b7a8a1764cac`。事务升级创建回滚包 `/var/lib/lodge-deploy-backups/hub-20260806T230955Z-46Yw1B` 和 post-deploy 备份 `/var/lib/lodge-hub/backups/post-deploy-20260806T230957Z-3789728.db`；SQLite schema 2 完整性为 `ok`。

## Agent 发布物和安全不变量

- Go 1.26.5、`CGO_ENABLED=0`、Linux amd64 静态 Agent。
- 首次纳管 SHA-256：`35f22e2cc8cfd3a04ebc345e3d5402277c3b34972bb6d431a2000a3093bccd64`；五机归属升级后的统一 SHA-256：`8b100846bee3318f01ec17ea3d1b3c2023d0963c5976ff9718049cfb3ce8582a`，版本 `0.2.0`。
- `lodge` 为 nologin 系统用户，不属于 `docker`、`sudo`、`wheel`、`adm`。
- Agent 与 Hub Go 进程只监听 loopback；Tailscale 使用 raw TCP Serve，避免依赖 Hub 的 MagicDNS/HTTP Host 路由。
- Agent token 和 Hub config 均为 `lodge:lodge 0600`。
- sudoers 由 Agent 固定 argv 生成，不使用全局 Alias；候选策略和全机策略增量均验证，`docker run` 越权测试失败是预期结果。
- root-only 自定义进程 helper 只读取 PID/UID、进程与可执行文件 basename、工作目录 basename 和不可逆目录指纹；不读取命令行、环境变量或完整路径，追加参数无法命中 Agent 与 sudoers 双重精确白名单。
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

## 五机归属升级

提交 `21a180a` 兼容 `/system.slice/docker-<id>.scope`，并将 host-network 容器在宿主 `ss` 中可见、但 Docker `.Ports` 没有声明的监听归回原容器。提交 `287c1b4` 增加脱敏进程来源采集：同一 UID、目录指纹和运行时的多个监听端口聚合为稳定的 `process:<fingerprint>` workload，进程重启不改变 key。

五台机器逐台执行预检、root-only 回滚备份、安装、自身服务上下文验收、越权测试和 Hub 端复验，前一台通过后才继续下一台。回滚点如下：

- tencent：`/var/lib/lodge-deploy-backups/agent-20260807T0019Z-287c1b4-oHbBFZ`
- banwagong：`/var/lib/lodge-deploy-backups/agent-20260807T0023Z-287c1b4-YmvlI3`
- bytebunny：`/var/lib/lodge-deploy-backups/agent-20260807T0027Z-287c1b4-JVSlUA`
- bytedragon：`/var/lib/lodge-deploy-backups/agent-20260807T0030Z-287c1b4-SLZTtH`
- ali：`/var/lib/lodge-deploy-backups/agent-20260807T0033Z-287c1b4-voH5l2`

bytebunny 与 ali 的旧 HTTP Serve 分别迁移为 Tailnet-only TCP Serve，变更前状态保存在 `/var/lib/lodge/tailscale-backups/20260807T002131Z-3393098` 和 `/var/lib/lodge/tailscale-backups/20260807T002400Z-1469120`。bytedragon 的 Hub 继续通过 loopback 访问本机 Agent，没有额外开放 8443；Hub 的 HTTPS Serve `10000` 未受影响。

tencent 原来 8 个逐端口 Node workload 被聚合为 `GridGo · node`、`daily · node`、`image · node`、`nas · node` 四个稳定 workload；bytebunny 的 Nginx 443 和 banwagong 的 `cpa-manager-plus:18317` 均归回 Docker。五台 root helper 的 JSONL 字段、指纹格式、basename 不含 `/`、追加参数拒绝均在 live 主机验证。所有精确远程 staging 目录已删除，回滚包保留。

bytebunny 也存在与 banwagong 相同的既有 `/etc/sudoers.d/hermes-ro` mode 0644 诊断；Lodge 同样没有修改或启用它，并确认安装前后诊断不增加。

HTTP→TCP 路由切换后，Hub 的 Go HTTP 连接池曾继续复用旧 Tailscale HTTP Serve 连接，使两台最新观测短暂记录为 404/offline；从 Hub 新建连接访问均成功。重启前创建并验证一致性 SQLite 备份 `/var/lib/lodge-hub/backups/pre-agent-route-reconnect-20260807T0027Z.db`（`lodge:lodge 0600`），连接池重建后首轮即恢复 5/5。提交 `3b7b486` 增加“404 后关闭空闲连接并仅重试一次”的有界恢复；401/403 不重试，连续 404 仍明确失败。该 Hub 已用事务安装器部署：生产 SHA-256 为 `50b251a0a9551134aa4e30b1184f69a3740914a9a5feb01eb11c2204e4256693`，回滚包 `/var/lib/lodge-deploy-backups/hub-20260807T130400Z-XvlKuH`，部署后备份 `/var/lib/lodge-hub/backups/post-deploy-20260807T130404Z-3860562.db`。原始离线历史保留，不能用删记录掩盖部署瞬态。

## Agent 0.2.0 全 fleet 归属基线

| Host | Online | Workloads | Endpoints | Unidentified |
| --- | ---: | ---: | ---: | ---: |
| bytedragon | 1 | 8 | 19 | 0 |
| bytebunny | 1 | 9 | 21 | 0 |
| ali | 1 | 5 | 11 | 0 |
| tencent | 1 | 12 | 21 | 0 |
| banwagong | 1 | 11 | 14 | 0 |
| **Total** | **5/5** | **45** | **86** | **0** |

该轮已归因 workload 为 45/45，即 100.0%，超过 95% 门禁。当时 Compose、完整 systemd/failed-unit 和 Caddy/Nginx 脱敏路由发现仍是产品完整性待办，但不再阻塞归因率验收。

该轮 SQLite 最新观测为 5/5 online、五台 Agent 均为 `0.2.0`、0 warnings，最大年龄 31 秒，`PRAGMA integrity_check=ok`。Hub config 仍为 `lodge:lodge 0600`，包含 `passwordHash` 而不含明文 `password`；未认证 `/api/agents` 返回 401；五枚 Agent token 均未出现在 SQLite、WAL 或 SHM。token 导入暂存文件、五机 Agent staging 和 Hub staging 目录均已删除。

## Compose 与完整 systemd/failed-unit 交付

提交 `cf2300f` 将 Hub 持久层升级到 SQLite schema 3，并在共享协议、领域模型、数据库和 Web 投影中加入可选 `composeProject`/`composeService`。v2→v3 迁移测试证明旧观测保留且新字段为空。systemd 发现改用固定 `systemctl show` 字段集：纳入 operator-managed active 单元和全部 failed 单元；无监听服务作为 workload 展示，活跃 vendor 单元仍默认折叠，但已通过套接字归属的 vendor 单元不会消失。fragment path 只在 Agent 内存中参与分类，不进入 API、SQLite 或 UI。

生产 Hub 静态二进制 SHA-256 为 `daeb509e9bca5c77e9a75196470094ff1f46046fb0ff6c870a80d37933aa67f3`；回滚包 `/var/lib/lodge-deploy-backups/hub-20260807T132822Z-D7irgu`，部署后一致性备份 `/var/lib/lodge-hub/backups/post-deploy-20260807T132825Z-3863789.db`。升级后 schema 3 完整性为 `ok`，五台旧 Agent 0.2.0 在滚动发布前仍保持 5/5 online，验证协议向后兼容。

Compose 发现只读取 Docker 官方 `com.docker.compose.project` 与 `com.docker.compose.service`。初版直接把复杂 Docker Go template 写进 sudoers：带引号版本被 `visudo` 拒绝，去掉引号后语法虽然通过，但真实 sudo argv 无法精确匹配。tencent 的服务上下文验收因此以 password-required warning 拒绝安装；该主机随即原子恢复到 Agent 0.2.0、12 services、0 warnings，其他四台没有部署这版候选。最终提交 `f69753f` 把 Compose 采集隔离到 root-owned Agent 的无动态参数自调用 `--collect-compose-metadata`：helper 内部执行固定 Docker 查询，只输出经过长度、字符与结构校验的 `container ID / project / service` 三元组；不读取或返回完整标签、环境变量、命令行、working dir、配置路径或任意调用者参数。stdout/stderr 和执行时间均有界，追加参数在 Agent 与 sudoers 两层都被拒绝。

五台最终 Agent 均为 `0.3.0`，CGo-free Linux amd64 静态二进制 SHA-256 均为 `4c16a53e82d4d19540233eacbf6860a0aa63ee1d966335317f8d71282770e874`。每台依次完成候选 checksum、独立 sudoers、root helper 输出、非特权服务 API、`NoNewPrivs=0`、越权拒绝、Tailnet 路由或 loopback、Hub 最新观测与 staging 清理验收；前一台完成后才进入下一台。回滚点为：

- tencent：`/var/lib/lodge-deploy-backups/agent-20260807T1346Z-f69753f-2EeaFo`
- banwagong：`/var/lib/lodge-deploy-backups/agent-20260807T-compose-f69753f-ZllZKU`
- bytebunny：`/var/lib/lodge-deploy-backups/agent-20260807T-compose-f69753f-iW4VNX`
- bytedragon：`/var/lib/lodge-deploy-backups/agent-20260807T-compose-f69753f-gOR7to`
- ali：`/var/lib/lodge-deploy-backups/agent-20260807T-compose-f69753f-dKjofX`

bytedragon 发布 Agent 时 Hub PID `3863962` 保持不变；本机 Agent 仍只通过 loopback 被 Hub 访问，没有新增 8443 Serve，Hub 的 Tailnet-only HTTPS 10000 正常。其他四台 Agent 的 Tailnet-only TCP 8443 检查均通过。bytebunny、banwagong 既有 `hermes-ro` 0644 诊断在安装前后 checksum、owner 和 mode 不变，Lodge 没有把旧授权意外启用。Ali 首轮发布后出现一次 Hub 到 Agent 的 EOF 离线历史，下一轮自动恢复；该瞬态保留在历史中，没有通过删记录掩盖。

## Agent 0.3.0 最新 fleet 验收

| Host | Online | Workloads | Endpoints | Warnings | Unidentified |
| --- | ---: | ---: | ---: | ---: | ---: |
| ali | 1 | 5 | 11 | 0 | 0 |
| banwagong | 1 | 14 | 14 | 0 | 0 |
| bytebunny | 1 | 13 | 21 | 0 | 0 |
| bytedragon | 1 | 10 | 19 | 0 | 0 |
| tencent | 1 | 13 | 21 | 0 | 0 |
| **Total** | **5/5** | **55** | **86** | **0** | **0** |

Compose 只出现 banwagong `new-api` project 的 `new-api`、`postgres`、`redis` 三项，均为 running；没有其他主机或字段误报。failed-unit 资产为 banwagong 的 `certbot.service`、`networking.service`、`rc-local.service`，以及 bytebunny 的 `fwupd-refresh.service`。动态 SSH 会话可能令 `user@0.service` 在某一轮出现或消失，因此验收以必需单元集合、状态和安全不变量为准，不把瞬时 workload 数量写成单机硬编码门禁。

最终点测为 5/5 online、全部 Agent `0.3.0`、55/55 workloads 已归因（100.0%）、86 endpoints、0 warnings、0 unidentified，最大观测年龄 28.7 秒。SQLite schema 3 `integrity_check=ok`；Hub config 为 `lodge:lodge 0600`，只含 `passwordHash`；五枚 Agent token 在 SQLite、WAL、SHM 中均无命中。五台精确 staging 目录均已删除，root-only 回滚包保留。M3 现在只剩 Caddy/Nginx 脱敏路由发现。

> [!note]
> tencent 与 banwagong 的 Tailscale `Tags` 当前为空。Serve/Funnel 验证证明管理端点不在公网，Agent bearer token 仍提供第二道边界；但 `tag:lodge-agent` 和“普通 tailnet 节点不能访问 8443”的负向 grants 尚未在管理控制台验收，因此最小权限 Tailnet ACL 仍是明确待办。

## CI 证据

- `d6e8a9c`：[quality 31129778895](https://github.com/toolazytoname/lodge/actions/runs/31129778895)
- `0a26321`：[quality 31130733131](https://github.com/toolazytoname/lodge/actions/runs/31130733131)
- `5cfa1f2`：[quality 31131263036](https://github.com/toolazytoname/lodge/actions/runs/31131263036)
- `21eb0b4`：[quality 31131614944](https://github.com/toolazytoname/lodge/actions/runs/31131614944)
- `0d77509`：[quality 31132153479](https://github.com/toolazytoname/lodge/actions/runs/31132153479)
- `cd165b7`：[quality 31132585365](https://github.com/toolazytoname/lodge/actions/runs/31132585365)
- `21a180a`：[quality 31133399706](https://github.com/toolazytoname/lodge/actions/runs/31133399706)
- `287c1b4`：[quality 31133951144](https://github.com/toolazytoname/lodge/actions/runs/31133951144)
- `3b7b486`：[quality 31180699616](https://github.com/toolazytoname/lodge/actions/runs/31180699616)
- `cf2300f`：[quality 31182553859](https://github.com/toolazytoname/lodge/actions/runs/31182553859)
- `f9b349f`：[quality 31183022460](https://github.com/toolazytoname/lodge/actions/runs/31183022460)（代码门禁通过，但 live sudo 精确匹配验收拒绝该方案）
- `f69753f`：[quality 31183613991](https://github.com/toolazytoname/lodge/actions/runs/31183613991)

所有相关提交均通过全量质量门禁、race detector 和 `govulncheck`；用于隔离 GitHub 延迟并发队列的临时 CI tags 在验收后均已删除。
