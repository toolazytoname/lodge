<p align="center">
  <strong>你的私人服务器 / 服务控制台。</strong>
  <br>
  hub + agent 架构，Go 单静态二进制。
</p>

<p align="center">
  <a href="README.en.md">English docs</a>
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/github/license/toolazytoname/lodge?style=flat-square" alt="License"></a>
  <img src="https://img.shields.io/badge/version-2.0.0-blue?style=flat-square" alt="Version">
  <img src="https://img.shields.io/badge/stack-Go-00ADD8?style=flat-square" alt="Stack">
</p>

---

## 这是什么

每台自己维护的服务器上跑着什么服务、暴露到哪里（只本机可访问 / 只 tailnet 内可访问 / 公网可达），一目了然——而不是靠手工表格，一周后就过时。

- **agent**：部署在每台被管机器上，非 root 账号运行，只读采集端口/容器/systemd 服务，通过 sudoers 精确白名单执行少数几个受限动作，不支持任意命令执行。
- **hub**：中心节点，定期拉取各 agent 的状态，提供 Web 界面统一查看；当前使用密码登录 + 登录限速。历史、告警、受控操作代理与 vault 仍在路线图中，未完成前不作为安全承诺。

## 架构

```
浏览器 ──HTTPS──> lodge-hub  ──Tailscale tailnet──> lodge-agent × N
                (Go，内嵌前端)                    (非 root，sudoers 白名单)
```

hub 主动拉取（非 agent 推送），机器都在同一个 tailnet 里；agent 只监听 `127.0.0.1`，通过 Tailscale Serve 仅向 tailnet 中获授权的 hub 提供数据，不能使用 Funnel 暴露到公网。

## 安全模型

- **agent 绝不以 root 运行**，专用系统账号，不加入 `docker` 组（docker 组等价 root）。特权操作走 `/etc/sudoers.d/lodge-agent` 的完整命令行精确匹配白名单，agent 内部用固定 argv 经 `exec.Command` 执行，不经 shell。
- **hub 登录**：Argon2id 慢哈希 verifier，不保存明文密码；独立随机密钥签名会话，Cookie 使用 `Secure`、`HttpOnly`、`SameSite=Strict`，写操作验证 CSRF token；连续失败按指数退避锁定。
- **管理面边界**：生产管理面只允许在 Tailnet 内开放；登录认证是纵深防御，不是公网暴露的许可。受控运维和 vault 仍在路线图中，未完成前不作为安全承诺。

## 开发

需要 Go 1.25.12 或更新的受支持工具链；CI 固定使用最新 Go 1.26.x。

```bash
go build ./...
GOOS=linux GOARCH=amd64 go build -o dist/lodge-agent ./cmd/lodge-agent
GOOS=linux GOARCH=amd64 go build -o dist/lodge-hub ./cmd/lodge-hub

npm test    # 本地完整质量门禁（format/vet/build/test/race/脚本与文档数据检查）
```

## 目录结构

```
cmd/lodge-agent/    agent 入口
cmd/lodge-hub/      hub 入口
internal/agent/     采集、服务发现关联、白名单动作
internal/hub/       API、存储、采集器、认证、登录限速
internal/shared/    共享类型
internal/hub/web/   前端（内嵌进 hub 二进制）
deploy/             systemd unit、sudoers 模板、install-agent.sh
docs/               架构、威胁模型、质量门禁与开发规范
quality/            可量化质量基线与目标
```

## 工程规范

- [架构](docs/architecture.md)
- [威胁模型](docs/threat-model.md)
- [Hub 认证配置与迁移](docs/hub-authentication.md)
- [Tailnet-only 部署](docs/tailnet-deployment.md)
- [质量门禁](docs/quality-gates.md)
- [开发与验收](docs/development.md)
- [实施路线](docs/roadmap.md)

## 贡献

欢迎小而专注的 PR。超过 typo 范围的改动，先开 issue 对齐方向。安全问题**别**开公开 issue —— 邮件 <lazywc@gmail.com>。

## License

[MIT](LICENSE)

<p align="center">
  Built by <a href="https://github.com/toolazytoname">@toolazytoname</a>
  · <a href="mailto:lazywc@gmail.com">lazywc@gmail.com</a>
</p>
