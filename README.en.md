# Lodge

<p align="left">
  <img src="assets/logo-wordmark.svg" alt="Lodge" width="180">
</p>

> Your private home base for servers, services, and secrets.

A self-hosted personal console. **One HTML file + one JSON file**. Zero servers, zero telemetry, end-to-end encrypted.

[中文文档](README.md) · [Live demo](https://lodge-iota.vercel.app/dashboard.html)

## Features

- **Three-in-one** — Server inventory, service catalog, encrypted vault
- **Zero network requests** — All CSS/JS inlined, no external dependencies
- **Zero-knowledge encryption** — AES-GCM 256, master password never leaves the browser
- **iCloud sync** — Drop the two files into iCloud Drive, access from any device
- **Responsive** — Phone, tablet, desktop

## Files

```
dashboard.html   The app (~90KB, single file)
config.json      Your data (servers / services / encrypted vault)
index.html       Marketing landing page (deploy to Vercel)
404.html         Custom 404
vercel.json      Vercel configuration
```

## Quick Start

### 1. Local development

```bash
git clone https://github.com/toolazytoname/lodge.git
cd lodge
python3 -m http.server 9001
# Open http://localhost:9001/dashboard.html
```

Or with Node:

```bash
npx serve -l 9001
```

### 2. Public access (HTTPS required)

The Web Crypto API only works in secure contexts. Three easiest paths:

| Option | Difficulty | Notes |
|--------|------------|-------|
| **Cloudflare Tunnel** | ⭐ Easiest | No account needed, 5 min, `trycloudflare.com` domain |
| **Tailscale Funnel** | Easy | Install clients, automatic HTTPS |
| **Caddy + domain** | Medium | Own domain + DNS, set-and-forget |

**Cloudflare Tunnel one-liner** (recommended):

```bash
# Install cloudflared
curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg | sudo tee /usr/share/keyrings/cloudflare-main.gpg >/dev/null
echo "deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared $(lsb_release -cs) main" | sudo tee /etc/apt/sources.list.d/cloudflared.list
sudo apt update && sudo apt install -y cloudflared

# Start tunnel (ensure dashboard.html is being served on :9001 in another terminal)
cloudflared tunnel --url http://localhost:9001
```

You'll get a `https://xxx.trycloudflare.com` URL — use that to access.

### 3. iCloud Drive cross-device sync

```bash
cp dashboard.html config.json ~/Library/Mobile\ Documents/iCloud~is~workflow/Documents/Lodge/
```

On iPhone:
1. Open **Files** app → iCloud Drive → **Lodge** folder
2. Long-press `dashboard.html` → Share → Open in Safari
3. First time, you'll be asked to select `config.json` — Safari remembers after that

> **Important**: `file://` protocol on iPhone doesn't support Web Crypto. **You must access via HTTPS** (Cloudflare Tunnel, Tailscale, etc.).

### 4. SSH connections

Each server card has two actions:

- **SSH button** — Tries to launch your system's default SSH client
  - macOS → Terminal/iTerm
  - iOS → Termius / Prompt
  - Falls back to copying the command to clipboard
- **Copy button** — Copies `ssh user@host -p port`, clears clipboard after 30s

## Security Model

| Aspect | Implementation |
|--------|----------------|
| Master password | Never uploaded, never stored — derived in memory only |
| Key derivation | PBKDF2-SHA256, 600,000 iterations |
| Data encryption | AES-GCM 256, unique IV per vault item |
| Password verification | `verifier` field confirms correct password |
| Memory protection | Plaintext cleared on lock, on tab close |
| Clipboard | Auto-clears 30s after copy (SSH and vault) |
| Attack surface | **Zero network requests, zero servers, zero third parties** |

### Attack scenarios

| Scenario | Consequence |
|----------|-------------|
| iCloud account compromised | Attacker gets ciphertext + metadata — vault stays sealed without master password |
| Config file leaked publicly | Server IPs exposed, but vault remains encrypted |
| Device lost | Active session could be read; use short auto-lock |
| Master password cracked | All tokens exposed — use a strong password |

### Known limitations

- **Metadata in `config.json` is plaintext** (server names, IPs, service URLs). This is intentional — without it, you can't see your services.
- **`localStorage` cache is plaintext** (except vault). Wipe it before switching devices via Settings → Clear local cache.
- **Browser history** records the URL. Use local deployment or Tailscale to avoid public traces.

## Data Format (config.json)

```json
{
  "version": 1,
  "meta": { "created": "ISO", "lastModified": "ISO" },
  "servers": [
    { "id": "nas", "alias": "Home NAS", "host": "192.168.1.10",
      "sshPort": 22, "sshUser": "root",
      "tags": ["home"], "notes": "..." }
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

## Daily Use

### Modify data
- Add/edit/delete servers, services, vault items in the UI
- Changes auto-save to localStorage (prevents accidental loss)
- Click **Save to file** in the footer → browser downloads `config-TIMESTAMP.json`
- Move the downloaded file to your Lodge directory to overwrite `config.json`
- iCloud syncs automatically

### Change master password
**Settings → Change master password** → current + 2× new → automatically re-encrypts all vault items.

### Forgot master password
**No recovery.** This is the cost of zero-knowledge design.

**Mitigation**:
- Write down your password physically and store it safely
- Don't store critical tokens only in Lodge

## Deploy to GitHub

### Privacy for public repos

If you deploy the HTML to GitHub Pages, **`config.json` must be sanitized**. Here's a clean template:

```json
{
  "version": 1,
  "meta": { "created": "2024-01-01T00:00:00.000Z", "lastModified": "2024-01-01T00:00:00.000Z" },
  "servers": [
    { "id": "demo-1", "alias": "Demo server", "host": "192.0.2.1",
      "sshPort": 22, "sshUser": "demo",
      "tags": ["example"], "notes": "public demo data" }
  ],
  "services": [
    { "id": "demo-svc", "name": "Demo service", "url": "https://example.com",
      "type": "web", "icon": "E", "serverId": "demo-1", "description": "" }
  ],
  "vault": {
    "kdf": "PBKDF2-SHA256", "iterations": 600000,
    "salt": "", "verifier": "", "items": []
  },
  "settings": { "autoLockMinutes": 5, "theme": "auto", "clearClipboardSeconds": 30 }
}
```

**Never commit**:
- Real `vault.salt` / `vault.verifier` — even encrypted, anyone with the master password can decrypt
- Real server IPs / domains — your internal topology shouldn't be public
- Real SSH usernames / ports / notes

**Recommended deployment**:
- HTML → GitHub Pages (HTTPS, free)
- `config.json` → your own server (exposed via Cloudflare Tunnel for HTTPS)
- HTML fetches your `config.json` (both HTTPS, no CORS issues)

## Browser Compatibility

Requires Web Crypto API:
- Safari 11+ / Chrome 60+ / Firefox 60+ / Edge 79+

## Out of Scope

Lodge deliberately **does not** do:
- Web SSH terminal (use `ssh://` deep links or local terminal)
- TOTP autofill (use 1Password / Authy)
- Multi-user / team collaboration
- Server-side health checks
- Automatic cloud sync (rely on iCloud / your own storage)
- File / image attachments

If these are hard requirements, consider Vaultwarden, Bitwarden, or 1Password.

## Development

Single HTML file with all CSS/JS inlined. Edit directly in any text editor. To split for maintainability, extract `<style>` and `<script>` blocks — no build step needed.

## License

MIT
