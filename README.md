<p align="center">
  <img src="assets/logo-wordmark.svg" alt="Lodge" width="220">
</p>

<p align="center">
  <strong>你的私人服务器、服务、密钥的「山间小屋」。</strong>
  <br>
  一个 HTML 文件 + 一个 JSON 文件。零服务器，零遥测，端到端加密。
</p>

<p align="center">
  <a href="https://lodge.weichao.studio">立即体验</a>
  ·
  <a href="#快速开始">快速开始</a>
  ·
  <a href="https://lodge.weichao.studio/about.html">关于页</a>
  ·
  <a href="README.en.md">English docs</a>
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/github/license/toolazytoname/lodge?style=flat-square" alt="License"></a>
  <img src="https://img.shields.io/badge/version-1.0.0-blue?style=flat-square" alt="Version">
  <img src="https://img.shields.io/badge/stack-HTML%20%2B%20CSS%20%2B%20JS-orange?style=flat-square" alt="Stack">
  <img src="https://img.shields.io/badge/crypto-AES--GCM%20256-green?style=flat-square" alt="Crypto">
  <img src="https://img.shields.io/badge/network-zero%20requests-9cf?style=flat-square" alt="Network">
  <img src="https://img.shields.io/badge/build-none-lightgrey?style=flat-square" alt="Build">
</p>

<br>

<p align="center">
  <img src="assets/og.png" alt="Lodge — 你的私人服务器、服务、密钥控制台" width="720">
</p>

---

## 先看效果

真实截图，来自一个干净的 dashboard。设置就一个主密码，下面这些就是本来要用表格记的数据。

<p align="center">
  <img src="assets/screenshots/dashboard-servers.png" alt="Lodge 服务器 tab —— 三台机器，别名、标签、一键 SSH" width="900">
</p>

<p align="center">
  <em>Servers tab —— 别名、标签、一键 SSH、剪贴板兜底。</em>
</p>

<br>

<p align="center">
  <img src="assets/screenshots/dashboard-services.png" alt="Lodge 服务 tab —— 五个服务按主机分组，支持搜索和类型筛选" width="900">
</p>

<p align="center">
  <em>Services tab —— 按主机分组，按类型筛选，毫秒级搜索。</em>
</p>

<br>

<p align="center">
  <img src="assets/screenshots/dashboard-dark.png" alt="Lodge 暗色模式 dashboard" width="900">
</p>

<p align="center">
  <em>暗色模式 —— 数据没变，跟随系统主题。</em>
</p>

---

## 一分钟版

每个折腾过自托管的人都会攒下三样东西：**机器、服务、token**。Lodge 把这三样东西收进同一个私人控制台 —— 一个 HTML 文件，任何浏览器都能打开，靠 iCloud Drive 同步，密钥在浏览器里加密完才落盘。

- 🖥 **服务器清单** —— 别名、标签、SSH 凭据，点一下唤起终端
- 🔗 **服务目录** —— 按主机分组 Web 应用、媒体服务器、管理面板，毫秒级搜索
- 🔒 **加密 Vault** —— AES-GCM 256，主密码只在浏览器内派生，PBKDF2-SHA256 60 万次迭代
- 📁 **两个文件** —— `dashboard.html` + `config.json`。无需 build、无需后端、无需注册

---

## 为什么是 Lodge？

每个替代方案都有点别扭：

| 你想做的事 | 你只能用… | 结果是… |
|---|---|---|
| 记住服务器 IP 和 SSH 端口 | 表格、便签 | 一周后就过时 |
| 给每个自托管服务做书签 | 浏览器书签 | 每次重置浏览器就丢 |
| 安全存 API token | 1Password、Bitwarden | 又一个订阅，又一个主密码 |
| 一眼看到「现在什么在跑」 | Heimdall、Homarr、Dashy | 为一个简单需求堆一整套 Docker |

Lodge = **上面四件事合在一个 HTML 文件里**。打开 DevTools → Network 标签 → 刷新：啥都不发生。这就是它全部的架构。

> *「丢两个文件，设一个密码，完事。」* —— 出自[关于页](https://lodge.weichao.studio/about.html)

---

## 功能

### 🖥 服务器清单

你运维的每一台机器，一张表搞定。

- 别名、主机名、SSH 端口和用户，一眼看清
- 每台机器的标签和自由备注
- 点一下 → 唤起系统默认 SSH 客户端（Terminal、iTerm、Termius、Prompt…）
- 唤起失败自动复制命令到剪贴板
- 复制 30 秒后自动清除剪贴板

### 🔗 服务目录

每个入口，一键直达。

- 图标、名称、URL、类型（media / web / admin / database…）
- 通过 `serverId` 一键关联到主机
- 几百个服务毫秒级搜索
- 新标签页打开，干净利落

### 🔒 加密 Vault

每个密钥，一个主密码。

- AES-GCM 256，每个条目独立 IV —— 相同明文产生不同密文
- PBKDF2-SHA256 密钥派生，60 万次迭代（OWASP 2023 基线）
- 主密码永不存储、永不上传、永不落盘 —— 只在内存里派生
- `verifier` 字段证明你输入对了密码，却不泄露任何内容
- 锁定 / 关页面时自动清空明文
- 复制 30 秒后自动清除剪贴板

### 📁 同步与多设备

- 把 `dashboard.html` 和 `config.json` 丢进 iCloud Drive / Dropbox / Syncthing → 所有设备看到同一份数据
- 或者自托管到 Vercel / Netlify / GitHub Pages / Tailscale Funnel / Cloudflare Tunnel
- `localStorage` 缓存：首次选完文件后，下次秒开
- 响应式：手机 / 平板 / 电脑，同一个 HTML 文件

---

## 快速开始

三种方式，挑一个顺手的。

### A. 直接用线上版 —— 零配置

👉 **<https://lodge.weichao.studio>**

根路径 301 重定向到 dashboard。所有现代浏览器都行：macOS / Windows / Linux / iPhone / iPad。

> 线上版只 serve HTML/JS 代码。你真实的 `config.json` 在**你自己的** iCloud Drive 里（或你放的地方）。攻击者访问这个 URL 只会看到一个空文件选择器。

### B. Clone 后双击打开 —— 不需要服务器

```bash
git clone https://github.com/toolazytoname/lodge.git
cd lodge
open dashboard.html        # macOS
xdg-open dashboard.html    # Linux
start dashboard.html       # Windows
```

Lodge 自动识别 `file://` 协议，走文件选择器 + `localStorage` 缓存路径。所有现代浏览器在 `file://` 下 Web Crypto API 都正常工作，Vault 全部功能可用。

### C. 自托管到你自己的 URL

需要从手机访问、或多人共用 HTTPS 链接时。

```bash
# Vercel（零配置，免费 HTTPS，可绑自定义域名）
npx vercel --prod

# GitHub Pages
# Settings → Pages → Deploy from branch → main / root

# Tailscale Funnel（连 DNS 都不用管）
tailscale funnel 9001

# Cloudflare Tunnel（连账号都不用注册，5 分钟搞定）
cloudflared tunnel --url http://localhost:9001
```

完整流程见 **[DEPLOY.md](DEPLOY.md)**。

---

## 工作原理

```
   ┌──────────────────────────────────────────────────────┐
   │  dashboard.html   （~90 KB 单文件，所有 CSS/JS        │
   │                    内联，零外部请求）                 │
   └─────────────────┬────────────────────────────────────┘
                     │
                     │  每次加载时读取
                     ▼
   ┌──────────────────────────────────────────────────────┐
   │  config.json     （你的数据 —— 服务器、服务、         │
   │                   Vault 密文、设置）                  │
   └─────────────────┬────────────────────────────────────┘
                     │
                     │  通过以下方式同步
                     ▼
   ┌──────────────────────────────────────────────────────┐
   │  iCloud Drive · Dropbox · Syncthing · 你自己的服务器  │
   │  （随你选，Lodge 不挑）                              │
   └──────────────────────────────────────────────────────┘
```

**每次加载，在浏览器里：**

1. 读取 `config.json`（从服务器、文件选择器、或 `localStorage` 缓存）
2. 展示服务器、服务、元数据 —— 全是明文（这是有意的，否则什么都看不到）
3. Vault 条目需要解锁时，弹出主密码输入框
4. 用 PBKDF2-SHA256（60 万次迭代）派生 AES 密钥 —— 永不落盘
5. 在内存里解密 Vault 条目，渲染到界面上
6. 锁定 / 关页面时清空明文

没有别的步骤。没有服务器。没有遥测。

---

## 安全模型

| 维度 | 实现 |
|---|---|
| 主密码 | 永不存储、永不上传 —— 只在内存里派生 |
| 密钥派生 | PBKDF2-SHA256，60 万次迭代（OWASP 2023） |
| 数据加密 | AES-GCM 256，每个 Vault 条目独立 IV |
| 密码验证 | `verifier` 字段证明密码正确，不解密任何东西 |
| 内存保护 | 锁定 / 关页面时清空明文 |
| 剪贴板 | 复制 30 秒后自动清除（SSH 和 Vault 都算） |
| 攻击面 | **零网络请求、零服务器、零第三方** |
| 遥测 | 无 |
| 统计 | 无 |

### 攻击场景对照

| 场景 | 后果 |
|---|---|
| iCloud 账号被黑 | 攻击者拿到密文 + 元数据，没有主密码解不开 Vault |
| 配置文件误传公网 | 服务器 IP 泄露，但 Vault 仍是密文 |
| 设备丢失 / 被偷 | 活动会话可能被读 → 把自动锁定调短点（默认 5 分钟） |
| 主密码被破解 | 所有 token 裸奔 —— 选一个强的，别复用 |
| CDN 供应链攻击 | 不存在 —— Lodge 没有 CDN 依赖 |

### 已知边界（有意的设计）

- **`config.json` 里的元数据是明文** —— 服务器名、IP、服务 URL。否则一打开 App 什么都看不到。如果连元数据都加密，那等于每渲染一次就要解一次。
- **`localStorage` 缓存里是明文元数据**（不含 Vault 密钥）。换设备 / 换用户前去 设置 → 清除本地缓存。
- **浏览器历史会记录访问 URL。** 想不留痕用本地部署、Tailscale、或临时隧道。

---

## 数据格式（`config.json`）

一个文件，带版本号，可手编。

```jsonc
{
  "version": 1,
  "meta": { "created": "2025-01-01T00:00:00Z", "lastModified": "2025-01-01T00:00:00Z" },

  "servers": [
    {
      "id": "nas",
      "alias": "家庭 NAS",
      "host": "192.168.1.10",
      "sshPort": 22,
      "sshUser": "root",
      "tags": ["家庭"],
      "notes": "FreeBSD，4 盘位"
    }
  ],

  "services": [
    {
      "id": "jellyfin",
      "name": "Jellyfin",
      "url": "http://192.168.1.10:8096",
      "type": "media",
      "icon": "J",
      "serverId": "nas",
      "description": "电影 & 剧集"
    }
  ],

  "vault": {
    "kdf": "PBKDF2-SHA256",
    "iterations": 600000,
    "salt": "<base64>",
    "verifier": "<base64>",
    "verifierIv": "<base64>",
    "items": [
      {
        "id": "uuid",
        "type": "token",
        "title": "GitHub PAT",
        "url": "https://github.com",
        "username": "you",
        "ciphertext": "<base64>",
        "iv": "<base64>",
        "createdAt": "ISO",
        "updatedAt": "ISO"
      }
    ]
  },

  "settings": {
    "autoLockMinutes": 5,
    "theme": "auto",
    "clearClipboardSeconds": 30
  }
}
```

UI 里改最安全；清楚自己在干啥也可以手编。脱敏模板见 [`config.example.json`](config.example.json)。

---

## 跨设备

| 模式 | 适合 | 怎么搞 |
|---|---|---|
| **线上 demo** | 试用、单设备 | 打开 <https://lodge.weichao.studio>，选你的 config |
| **iCloud Drive** | 个人，1-3 台 Apple 设备 | 把两个文件丢 iCloud Drive，从任一设备打开 |
| **Dropbox / Syncthing** | 多系统混用家庭 | 跟 iCloud 一样 |
| **Vercel + 自己的 config 服务器** | 想用「正经」HTTPS 公网链接 | HTML 推 Vercel，`config.json` 通过 Cloudflare Tunnel 提供 |
| **Tailscale Funnel** | 个人，无 DNS 配置 | `tailscale funnel 9001` → `https://机器名.tail-net.ts.net` |
| **Cloudflare 临时隧道** | 临时公网链接 | `cloudflared tunnel --url http://localhost:9001` |

每台设备首次打开会弹文件选择器；之后 `localStorage` 缓存。想换 config：设置 → 清除本地缓存。

---

## 对比一下

| | Lodge | Bitwarden / Vaultwarden | Heimdall / Homarr | 表格 |
|---|---|---|---|---|
| 服务器清单 | ✅ | ❌ | 部分（书签） | ✅ |
| 服务目录 | ✅ | ❌ | ✅ | ✅ |
| Token Vault | ✅ | ✅（它们的主业） | ❌ | ❌（明文） |
| 零知识加密 | ✅ | ✅ | ❌ | ❌ |
| 自托管 | ✅（一个 HTML） | 需要 Docker | 需要 Docker | 没问题 |
| Build 步骤 | 无 | 无 | 无 | 无 |
| 需要后端 | 不需要 | 需要 | 需要 | 不需要 |
| 单文件 | ✅ | ❌ | ❌ | ✅ |
| 手机友好 | ✅ | ✅ | 部分 | ✅ |
| 离线可用 | ✅（file://） | ❌ | ❌ | ✅ |
| 成本 | 免费 | 免费 / $10/年 | 免费 | 免费 |

**什么时候选别的工具：**
- 需要 TOTP 自动填充 → **1Password** / **Bitwarden** / **Authy**
- 需要 Web SSH 终端 → **Termius** / **Shellngn**
- 需要多用户 / 团队面板 → **Homarr** + **Vaultwarden**

---

## 不在范围内

Lodge 明确**不**做：

- Web SSH 终端（用 `ssh://` 深链或本地终端）
- TOTP 自动填充（用 1Password / Bitwarden / Authy）
- 多人 / 团队协作
- 服务端健康检查（自己 ping）
- 自动云同步（用 iCloud / Syncthing）
- Vault 里的文件 / 图片附件
- 浏览器扩展

以上是硬需求的话你不是目标用户 —— 这没问题。Lodge 服务的是「宁愿用一个完全看得懂的 HTML 文件，也不想再多审计一个 SaaS」的人。

---

## 常见问题

**忘记主密码怎么办？**
无解。这就是零知识的代价。物理抄一份、藏好，这就是设计。

**为什么不上 WebAuthn / Passkey？**
因为 Lodge 没有服务器可以注册凭据。等生态对 local-first passkey 的支持稳定了再说。

**iPhone 上能用吗？**
能用，但要用**线上 demo**或 **Tailscale Funnel** URL —— Safari 在 `file://` 下会禁用 Web Crypto，Vault 就废了。设置页和 dashboard 能用，只有加密 Vault 需要 secure context。

**Lodge 会偷偷上报吗？**
不会。打开 DevTools → Network → 硬刷新 —— 每次加载都静悄悄。

**能从其他工具迁过来吗？**
手写你的 `config.json`，schema 上面已经写了。现在还没做导入器，欢迎 PR。

**怎么备份？**
`config.json` **就是**备份。随便拷 —— iCloud Drive、U 盘、邮箱。丢了主密码，Vault 就回不来；丢了文件，其他全没。

**有 CLI 吗？**
没有。Lodge 故意只有一个静态页，加 CLI 就违背「一个文件」的承诺了。

---

## 路线图

- [x] 单文件静态 SPA
- [x] 零知识 Vault（AES-GCM 256）
- [x] iCloud / Dropbox / 自定义服务器同步
- [x] 一键 SSH 唤起
- [x] 响应式布局
- [x] 主题（auto / light / dark）
- [x] 剪贴板自动清除
- [x] 每设备 `localStorage` 缓存
- [ ] 导入器：Bitwarden CSV、1Password 导出
- [ ] 可选只读公开分享（比如分享单个服务 URL，不暴露 dashboard）
- [ ] WebAuthn / Passkey 解锁（等 local-first 支持稳定后）

有想法？[开个 issue](https://github.com/toolazytoname/lodge/issues)。

---

## 开发

单 HTML 文件。所有 CSS 和 JS 内联。无 build 步骤。

```bash
git clone https://github.com/toolazytoname/lodge.git
cd lodge
python3 -m http.server 9001
# 打开 http://localhost:9001/dashboard.html
```

**改 App** —— 任何文本编辑器打开 `dashboard.html`。`<style>` 和 `<script>` 在文件底部；想拆分维护就抽出来。

**测试** —— `scripts/` 里有 Playwright 端到端测试：

```bash
npm install
npm test            # 单元 + 集成
npm run test:e2e    # 完整浏览器流程
npm run lint:js     # 语法检查
```

**目录结构**

```
dashboard.html       应用本体（~90 KB 单文件，所有 CSS/JS 内联）
config.example.json  脱敏示例 config（可提交到 git）
about.html           宣传 / 关于页（部署在 /about.html）
vercel.json          重定向 + CSP / 安全头
scripts/             Playwright E2E + shell 测试
assets/              Logo SVG + og.png 社交预览图
DEPLOY.md            部署流程（GitHub + Vercel + 自托管）
PRODUCTION.md        参考 URL（代码不读这个）
LICENSE              MIT
```

---

## 贡献

欢迎小而专注的 PR。提 PR 之前：

1. 搜一下已有 issue / PR
2. 超过 typo 范围的，先开个 issue 对齐方向
3. 守住「单文件」承诺 —— 不加 build 步骤，不加新依赖，除非真有必要

安全问题**别**开公开 issue —— 邮件 <lazywc@gmail.com>。

---

## License

[MIT](LICENSE)

---

<p align="center">
  Built by <a href="https://github.com/toolazytoname">@toolazytoname</a>
  · <a href="mailto:lazywc@gmail.com">lazywc@gmail.com</a>
</p>
