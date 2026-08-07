---
type: source
date: 2026-08-07
status: complete
sources:
  - current embedded Web source audit
  - live 5-host and 55-service sanitized browser fixture
  - generated Go-to-TypeScript contract and quality gates
---

# 2026-08-07 M4 Web 控制台

## 设计判断与现状审计

这是面向单个服务器运维者的高密度控制台，不是营销页。设计方向固定为冷静、可信、快速定位风险的深色 devtool 语言，参数为 `DESIGN_VARIANCE 4 / MOTION_INTENSITY 3 / VISUAL_DENSITY 8`。保持单一青绿色强调、统一圆角、数字等宽和语义化风险色；动画只用于反馈与状态转换，不做装饰。

使用 live Hub 的脱敏 API 结果构造本地浏览器 fixture，当前页面成功渲染 5 台主机和 55 行服务且无 console error。视觉与 DOM 审计发现：没有 Overview/Hosts/Services 信息架构；55 行服务只能整页滚动，没有搜索或过滤；长端口列表横向挤压操作按钮；主链接和路由重复；failed unit 没有风险优先级；逐行编辑按钮噪声较高；注解仍使用 `window.prompt`；加载、空、离线和上下文错误状态不完整。

## TypeScript 契约与构建底座

首个 M4 切片保留 Go 单二进制和无前端运行时框架的部署模型，只新增 lockfile 固定的构建期 TypeScript 6.0.3。`frontend/src/app.ts` 以 strict、`exactOptionalPropertyTypes` 和 `noUncheckedIndexedAccess` 编译为原生 ES module；浏览器行为在真实数量 fixture 上保持 5 cards / 55 rows / 0 errors。

Hub 将会话、主机摘要、服务分组和注解输入提升为明确的导出 HTTP 契约。`cmd/lodge-web-types` 通过 Go reflection 生成 `api.generated.d.ts`，包括共享 Service、Port、ProxyRoute、Exposure 与 Kind；Exposure/Kind 生成闭合 union，而不是宽泛 string。生成器 `--check` 和临时目录 TypeScript 编译会比较 committed `app.js`，任何 Go/TypeScript/构建产物漂移都阻断质量门禁。

`/api/services` 不再在 `agent` 字段重复发送完整 status 和原始 service 数组，只返回 ID、名称、在线状态、last seen/error 和 Agent 版本，再附一份 annotation-joined service views。回归测试验证紧凑契约和禁止重复字段。注解 JSON body 也只接受 alias、URL、hidden、notes；Agent 与 service identity 继续只来自已校验 query 参数。

CI 新增 Node 24 与 `npm ci`，本地和 CI 的 `npm test` 现在都验证 Go 格式、静态分析、单元/竞态测试、部署策略、生成类型、严格 TypeScript 编译、编译产物一致性和 JS 语法。TypeScript 是唯一 npm 依赖且只存在于 devDependencies；浏览器 bundle 没有第三方运行时代码。

## 后续切片

- 建立 Overview、Hosts、Services、Security、Operations 页面壳；未实现页面必须明确标为计划中，不能伪装成可用。
- 先完成 Overview 与 Services：全局搜索、主机/暴露/状态筛选、风险摘要、路由快速打开和无横向溢出的服务目录。
- 将 prompt 替换为可访问的注解对话框，支持 alias、URL、notes，并覆盖验证错误和保存状态。
- 为 loading、empty、offline、partial failure 和 error 建立 fixture、端到端与视觉回归；在 390、1280、1920 宽度验收。

## 五页面产品壳验收

第二个切片建立固定侧边导航、页面级标题与 Overview、Hosts、Services、Security、Operations 五个清晰边界。Overview 使用真实观测计算在线主机、工作负载、已发现 http(s) 链接和关注项，将 failed service 提到首屏，并对 Web 入口去重。Hosts 展示资源水位、服务/公网数量、Agent 版本和最后同步时间。Services 将 55 项高密度目录改为全局搜索、主机/暴露范围/状态筛选，长端口折叠成紧凑摘要，并把失败项排序到前面。逐行 prompt 被可访问的原生 dialog 替代，能够编辑 alias、URL、notes、hidden，非法协议或含凭据地址在提交前被拒绝。

Security 只汇总当前公网监听、Web 链接、待归因与离线节点；登录失败来源与历史告警明确标为 M5。Operations 只展示当前资产同步和 Agent 版本；重启、部署、回滚明确标为 M6 且保持只读。这样用户能看到完整信息架构，但不会把路线图能力误认为已经可用。

使用 1280 宽度、5 主机、55 服务的脱敏 live fixture 进行浏览器验收：五个页面均可切换；服务目录为 55/55；搜索 `certbot` 得到 1/55；四个 failed unit 具有最高风险视觉优先级；配置对话框打开后焦点进入首字段，`ssh://example.com` 被 URL 边界拒绝；页面 scroll width 等于 client width；console error 为 0。390 与 1920 宽度以及 loading/empty/offline/partial failure 的自动 fixture 仍是下一切片，因此相关路线图门禁尚未勾选。

## 响应式、异常状态与视觉门禁

2026-08-08 建立完全虚构且可重复的 5 主机/55 服务 fixture，不使用 live 主机名、IP 或 URL。浏览器矩阵固定时间、`zh-CN` locale、上海时区和 Chromium，覆盖 390、1280、1920 三种宽度；reference screenshots 包括移动服务目录、桌面服务目录、宽屏 Overview、离线 Overview 和全错误 Overview。

首次全错误不再显示会被误解为真实资产的 0/0，而是四项 `N/A` 和明确的“数据暂不可用”；首次 services 部分失败仍保留成功的 5 台主机信息，服务指标和目录只标记对应数据不可用；成功返回空数组才显示“尚未纳管”。离线 fixture 显示 4/5 并将连接超时提升为首屏风险信号。

端到端门禁当前包含 4 个测试、9 个关键场景和 5 张视觉基线：55 项密度、失败优先级、搜索、dialog 焦点、非法协议拒绝、五页移动导航、三宽度无横向溢出、empty/offline/partial/error 语义均通过。测试与配置也经过 strict TypeScript；fixture server 通过 Node 语法检查。M4 的页面、响应式、状态与视觉回归路线项完成，但注册 Web 链接主动可达率仍未测量，M4 整体继续保持 in-progress。

## Hub 视角主动入口检查

2026-08-08 实现显式的 `检查入口` 操作与 SQLite schema 5。Hub 从当前去重服务视图取得注解、脱敏代理路由与保守端口推断产生的 http(s) URL；一次最多 64 个、8 并发，每个只发一个最多 5 秒的 `HEAD`，总请求预算 15 秒。不跟随 redirect、不使用环境代理、不带凭据、不读取 body，持久化时只保留状态、HTTP 状态码、延迟、检查时间与 `timeout/tls/dns/connect/network` 等小范围错误分类。

100–499 表示“Hub 收到 HTTP 响应”，包括登录页、重定向和权限拒绝；500–599 表示 degraded；网络/TLS/DNS/超时表示 unreachable。它不声称公网可达、业务语义正常或用户浏览器一定可达。POST 需要登录与 CSRF，同一时刻只运行一轮；最新完整结果集原子替换，响应正文、header、解析 IP 与原始错误从不保存。

前端 Go→TypeScript 生成契约同步增加精确状态 union。Overview 指标、快速入口和服务行显示未检查/Hub 可达/5xx/Hub 不可达；offline fixture 明确出现 4/5 Hub 可达，全错误仍为 N/A。Playwright 增加一次带 fixture CSRF 校验的真实点击场景，因此关键场景从 9 增至 10，并重新生成、人工检查 390/1280/1920 视觉基线。实现门禁已通过；真实 Hub 部署和生产视角测量仍是 M4 完成前的最后证据。

## 生产发布与真实基线

提交 `1a8a698` 的 push/PR 两套 GitHub CI 均通过完整质量门禁与 `govulncheck` 后，使用 Go 1.26.5、CGo-free x86-64 Hub `0.5.0` 事务发布到 bytedragon，SHA-256 为 `cc3ee4bf9c65b61d088a1d0eb4e3d096169920534010d96afdf92edaa22274fa`。安装器创建 root-only 回滚包 `/var/lib/lodge-deploy-backups/hub-20260807T165354Z-SYGxkc` 与一致性备份 `/var/lib/lodge-hub/backups/post-deploy-20260807T165358Z-3896860.db`；schema 4→5、`integrity_check=ok`、5/5 online、55 workloads、11 routes、0 warnings、0 unidentified 和 Tailnet-only Serve 均通过，未触发回滚。

首次生产 Hub 检查覆盖 16 个当前展示入口：7 reachable、1 degraded、8 unreachable。按质量指标限定到 11 条实际发现的 Caddy/Nginx 路由：6 reachable、1 degraded、4 unreachable，即 54.5%，低于 95% 目标。主要证据类别包括 DNS、TLS、timeout 和一条 HTTP 502；原始错误未保存。M4 的“能测量、能辨别、能如实展示”能力完成，但资产质量不因功能完成而视为达标，后续必须逐条修复或退役错误路由。
