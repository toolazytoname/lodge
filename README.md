# Lodge

<p align="left">
  <img src="assets/logo-wordmark.svg" alt="Lodge" width="180">
</p>

> 自托管的个人控制台 —— 一个 HTML 文件 + 一个 JSON 文件，管理服务器、服务订阅、加密 token。

Lodge = 山间小屋（home base） + 寄托存放（lodge something）。集中托管你日常会用到的所有"个人基础设施"。

[English docs](README.en.md)

## 特性

- **三合一**：服务器清单 / 服务目录 / 加密 Vault
- **零网络请求**：HTML 内联所有 CSS/JS，无任何外部依赖
- **零知识加密**：Vault 内容 AES-GCM 256 加密，主密码永不离开浏览器
- **iCloud 同步**：把两个文件丢进 iCloud Drive，自动跨设备同步
- **响应式**：手机 / 平板 / 电脑自适应

## 文件

```
dashboard.html   应用本体（~90KB 单文件）
config.json      数据（服务器/服务/加密 Vault）
```

## 快速开始

### 1. 部署

把两个文件放在任意 HTTPS 可达的目录。最简单的方式：

```bash
# macOS 本地
mkdir -p ~/Documents/Lodge
cp dashboard.html config.json ~/Documents/Lodge/
cd ~/Documents/Lodge && python3 -m http.server 9001

# Linux 服务器
mkdir -p /opt/lodge
cp dashboard.html config.json /opt/lodge/
cd /opt/lodge && python3 -m http.server 9001
```

浏览器访问 `http://localhost:9001/dashboard.html`。

### 2. 公网访问（必须 HTTPS）

由于浏览器安全模型限制，**HTTP 公网下无法使用 Vault**（Web Crypto API 不可用）。三种最简单方案：

| 方案 | 难度 | 备注 |
|------|-----|------|
| **Cloudflare Tunnel** | ⭐ 最简单 | 免账号、5 分钟、`trycloudflare.com` 域名 |
| **Tailscale Funnel** | 简单 | 需装客户端、自动 HTTPS |
| **Caddy + 域名** | 中等 | 自有域名 + DNS，一劳永逸 |

**Cloudflare Tunnel 一行启动**（推荐）：

```bash
# 安装 cloudflared
curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg | sudo tee /usr/share/keyrings/cloudflare-main.gpg >/dev/null
echo "deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared $(lsb_release -cs) main" | sudo tee /etc/apt/sources.list.d/cloudflared.list
sudo apt update && sudo apt install -y cloudflared

# 启动隧道（先确保 dashboard.html 已在另一终端通过 http.server 9001 提供）
cloudflared tunnel --url http://localhost:9001
```

会输出 `https://xxx.trycloudflare.com`，用这个 URL 访问即可。

### 3. iCloud Drive 跨设备同步

```bash
# macOS
cp dashboard.html config.json ~/Library/Mobile\ Documents/iCloud~is~workflow/Documents/Lodge/
```

iPhone 上：
1. 打开"文件"App → iCloud Drive → Lodge 文件夹
2. 长按 `dashboard.html` → 共享 → Safari 打开
3. 首次会要求选 `config.json`，授权后 Safari 会记住

> **重要**：iPhone 上 file:// 协议不支持 Web Crypto。**必须**通过 Cloudflare Tunnel / Tailscale 等 HTTPS 方式访问。

### 4. SSH 连接

服务器卡片提供两种方式：

- **SSH 按钮**：尝试唤起系统默认 SSH 客户端
  - macOS → Terminal/iTerm
  - iOS → Termius / Prompt 等
  - 失败时自动复制命令到剪贴板
- **复制按钮**：复制 `ssh user@host -p port`，30 秒后自动清除

## 安全模型

| 维度 | 实现 |
|------|------|
| 主密码存储 | 永不上传、不落盘，仅在内存中派生 |
| 密钥派生 | PBKDF2-SHA256，600,000 次迭代 |
| 数据加密 | AES-GCM 256，每条 vault 独立 IV |
| 密码验证 | 启动时用 `verifier` 字段确认主密码 |
| 内存保护 | 锁定时清空明文，关闭页面自动清 |
| 剪贴板 | 复制后 30s 自动清（SSH 与 vault） |
| 攻击面 | **零网络请求、零服务器、零第三方** |

### 攻击场景对照

| 场景 | 后果 |
|------|------|
| iCloud 账号被黑 | 拿到密文 + 元数据，没有主密码解不开 vault |
| 配置文件误传公网 | 服务器 IP 泄露，但 vault 仍是密文 |
| 设备丢失 | 锁定前未关闭可能被读；建议短自动锁定 |
| 主密码被破解 | 全部 token 裸奔——主密码要强 |

### 已知边界

- `config.json` 中的**元数据**（服务器别名、IP、服务 URL）以明文存储，**这是设计如此**——不存明文的话你打开 app 就什么都看不到。
- 浏览器 `localStorage` 缓存同样的元数据（不含 vault 密钥）。切换设备/用户前可点 设置 → 清除本地缓存。
- 浏览器历史会记录访问 URL。强烈建议本地部署或 Tailscale，避免公网留痕。

## 数据格式 (config.json)

```json
{
  "version": 1,
  "meta": { "created": "ISO", "lastModified": "ISO" },
  "servers": [
    { "id": "nas", "alias": "家庭 NAS", "host": "192.168.1.10",
      "sshPort": 22, "sshUser": "root",
      "tags": ["家庭"], "notes": "..." }
  ],
  "services": [
    { "id": "jf", "name": "Jellyfin", "url": "http://192.168.1.10:8096",
      "type": "media", "icon": "J", "serverId": "nas",
      "description": "..." }
  ],
  "vault": {
    "kdf": "PBKDF2-SHA256",
    "iterations": 600000,
    "salt": "base64...",
    "verifier": "base64...",
    "verifierIv": "base64...",
    "items": [
      { "id": "...", "type": "token", "title": "...",
        "url": "...", "username": "...",
        "ciphertext": "base64...", "iv": "base64...",
        "createdAt": "ISO", "updatedAt": "ISO" }
    ]
  },
  "settings": {
    "autoLockMinutes": 5,
    "theme": "auto",
    "clearClipboardSeconds": 30
  }
}
```

## 日常使用

### 修改数据

- UI 里增删改服务器 / 服务 / Vault 条目
- 改动实时保存到 localStorage（防意外丢失）
- 改完点底部"保存到文件" → 浏览器下载 `config-时间戳.json`
- 把下载文件移动到 Lodge 目录覆盖原 `config.json`
- iCloud 自动推送到所有设备

### 跨设备使用（iPhone / iPad 等）

Lodge 支持两种跨设备模式：

**模式 A：部署到 Vercel（iPhone 推荐）**

1. **真实数据放在你 iCloud Drive。** 仓库的 `config.json` 已通过 `.vercelignore` 排除在 Vercel 部署之外。
2. 在任何设备的浏览器打开 `https://lodge-iota.vercel.app/dashboard.html`
3. **每台设备首次**：弹出文件选择器 → 进 iCloud Drive → 选你的真实 `config.json`
4. **之后**：localStorage 缓存，下次直接打开就加载
5. 改完点"保存到文件" → 把下载的文件替换 iCloud 那个 → 其他设备下次选文件时就看到最新数据

**为什么安全**：
- Vercel 只 serve HTML/JS 代码，没有真实数据
- 真实 config.json 只在你 iCloud Drive
- Vault 条目用主密码 AES-GCM 加密
- 攻击者访问 Vercel URL 只看到空文件选择器，啥也拿不到

**模式 B：自家服务器（自托管）**

如果你想要完全控制，部署在自己服务器，用以下任一方式暴露 HTTPS：

- **Tailscale Funnel**：`tailscale funnel 9001` → `https://机器名.tail-net.ts.net`
- **Cloudflare Tunnel**：免账号、5 分钟、临时 `trycloudflare.com` 域名
- **Caddy + 域名 + DNS**：生产级

用户体验和模式 A 一样。

**localStorage 缓存机制**

- 每台设备**首次**：手动选 config.json
- **之后**：自动从 localStorage 加载
- 想换 config：设置 → 清除本地缓存 → 下次打开会再弹选择器
- 跨设备同步：保存 → 替换 iCloud 文件 → 其他设备下次选文件时拿到新版本

**file:// 直开模式（高级，不需要服务器）**

也可以直接双击 `dashboard.html` 不开任何服务器，但有两个限制：
1. **保险库在 file:// 下失效**（Web Crypto 需要 secure context）
2. `fetch('./config.json')` 失败 → 必须用文件选择器（但 localStorage 缓存能解决这问题）

**iPhone 上怎么在 Safari 打开 file:// HTML**：
- 长按 `dashboard.html` → 共享 → **用 Safari 打开**

**iPhone 上想要完整功能（保险库也能用），用模式 A 或 B。**

### 更换主密码

设置 → 更改主密码 → 当前密码 + 两次新密码 → 自动用新密码重新加密所有 vault 条目。

### 忘记主密码

**无解**。这是零知识设计的代价。

**应对**：
- 设置时记牢
- 写一份物理备份（藏起来）
- 或者干脆不存重要 token 在 vault 里（用其他密码管理器）

## 部署到 GitHub

### 公开仓库的隐私规范

如果想把 HTML 部署到 GitHub Pages，**`config.json` 必须脱敏**。一个标准的脱敏模板：

```json
{
  "version": 1,
  "meta": { "created": "2024-01-01T00:00:00.000Z", "lastModified": "2024-01-01T00:00:00.000Z" },
  "servers": [
    { "id": "demo-1", "alias": "示例服务器", "host": "192.0.2.1",
      "sshPort": 22, "sshUser": "demo",
      "tags": ["示例"], "notes": "公开示例数据" }
  ],
  "services": [
    { "id": "demo-svc", "name": "示例服务", "url": "https://example.com",
      "type": "web", "icon": "E", "serverId": "demo-1", "description": "" }
  ],
  "vault": {
    "kdf": "PBKDF2-SHA256", "iterations": 600000,
    "salt": "", "verifier": "", "items": []
  },
  "settings": { "autoLockMinutes": 5, "theme": "auto", "clearClipboardSeconds": 30 }
}
```

**永远不要提交**：
- 真实的 `vault.salt` / `vault.verifier` —— 即便加密，谁拿到主密码都能解
- 真实的服务器 IP / 域名 —— 你的内网拓扑不该公开
- 真实的 SSH 用户名 / 端口 / 备注

**推荐部署模式**：
- HTML → GitHub Pages（HTTPS，免费）
- config.json → 你自己的服务器（通过 Cloudflare Tunnel 暴露 HTTPS）
- HTML fetch 你的 config.json（两端都 HTTPS，无 CORS）

## 浏览器兼容

需要支持 Web Crypto API 的现代浏览器：

- Safari 11+ / Chrome 60+ / Firefox 60+ / Edge 79+

## 不在范围内

明确**不**做：
- Web 终端 SSH（用 `ssh://` 深链或本地终端）
- TOTP 自动填充（用 1Password / Authy）
- 多人/团队协作
- 服务端状态探活后端
- 自动云同步（靠 iCloud / 自己的存储）
- 附件/图片存储

如果以上是硬需求，建议改用专业工具（Vaultwarden / Bitwarden / 1Password）。

## 开发与定制

HTML 是单文件，所有 CSS/JS 内联。直接用文本编辑器修改即可。要拆分维护，把 `<style>` 和 `<script>` 抽出来即可，无构建步骤。

## License

MIT
