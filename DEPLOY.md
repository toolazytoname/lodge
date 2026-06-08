# Deploying Lodge

This guide walks through publishing Lodge to GitHub and Vercel.

## 1. Push to GitHub

### Create the repo on GitHub

1. Go to https://github.com/new
2. Repository name: **`lodge`**
3. Description: `Your private home base for servers, services, and secrets`
4. Visibility: **Public** (so Vercel can deploy it) or Private (you'll add Vercel manually)
5. **Do not** initialize with README, license, or .gitignore (we already have these)
6. Click **Create repository**

### Push from your machine

GitHub will show you a "push an existing repository" command. Use this:

```bash
cd /path/to/lodge
git remote add origin https://github.com/toolazytoname/lodge.git
git push -u origin main
```

If 2FA is enabled, you'll need a [Personal Access Token](https://github.com/settings/tokens) instead of a password. Use the token as the password when prompted.

### Verify the push

Visit `https://github.com/toolazytoname/lodge` — you should see all 9 files.

## 2. Deploy to Vercel

### Option A: One-click via Vercel dashboard (easiest)

1. Go to https://vercel.com/new
2. **Import** the `toolazytoname/lodge` repository
3. Framework preset: **Other**
4. Root directory: `./` (default)
5. Click **Deploy**
6. Wait ~30 seconds
7. You'll get a URL like `https://lodge-xxxxx.vercel.app`

### Option B: Vercel CLI (for power users)

```bash
npm i -g vercel
cd /path/to/lodge
vercel login
vercel --prod
```

### Configure custom domain (optional but recommended)

1. In Vercel dashboard → your project → **Settings** → **Domains**
2. Add your domain (e.g., `lodge.example.com`)
3. Follow DNS instructions

Vercel auto-provisions HTTPS certificates — no extra work.

**Example (this repo's deployed URL):**
- Marketing: `https://lodge.weichao.studio/`
- App: `https://lodge.weichao.studio/dashboard.html`

After setting up a custom domain, update `og:image` URL in `index.html` and any "live demo" links in the README files to use the custom domain (the default `vercel.app` URL still works but is less professional).

## 3. What gets deployed

Vercel serves these files at the corresponding paths:

| File | URL |
|------|-----|
| `index.html` | `/` (marketing landing page) |
| `dashboard.html` | `/dashboard.html` (the app) |
| `config.json` | `/config.json` (example data only) |
| `404.html` | any unknown path (custom 404) |

⚠️ **Important**: The `config.json` on Vercel contains **example demo data only**. It does NOT contain your real servers, services, or vault data. For real data, follow step 4.

## 4. Use your real data

### For local-only use (recommended)

Don't put real data on Vercel. Run Lodge locally or on your own server:

```bash
# Clone (or copy) the two files
cp dashboard.html ~/Documents/Lodge/
cp config.json ~/Documents/Lodge/

# Edit config.json with your real data
# Then serve
cd ~/Documents/Lodge && python3 -m http.server 9001
```

Access via `http://localhost:9001/dashboard.html`.

### For cross-device access (your real data on your own server)

You need HTTPS for Web Crypto to work. Three options:

**Cloudflare Tunnel** (easiest, 5 min, no account):
```bash
# On your Linux server
cloudflared tunnel --url http://localhost:9001
# Get https://xxx.trycloudflare.com URL
```

**Tailscale Funnel** (recommended for personal use):
```bash
# On your server
tailscale funnel 9001
# Get https://machine-name.tail-net.ts.net
```

**Caddy + domain** (production):
```
# /etc/caddy/Caddyfile
lodge.yourdomain.com {
    reverse_proxy localhost:9001
}
```

## 5. Privacy checklist before going public

Before making your GitHub repo public:

- [x] `config.json` only has placeholder data (家庭 NAS, 192.168.1.x, example.com)
- [x] `.gitignore` ignores `config.real.json`, `config.local.json`, etc.
- [x] No real `vault.salt` or `vault.verifier` in the committed file
- [x] No API keys, tokens, or real credentials anywhere

If you ever commit real data by accident:
```bash
# Remove from history (CAUTION: rewrites all commits)
git filter-repo --path config.json --invert-paths
git push --force
```

Then immediately rotate your master password (the old one is now in git history).

## 6. Continuous deployment

After initial setup, Vercel auto-deploys on every push to `main`:

```bash
# Make changes
git add .
git commit -m "fix: ..."
git push
# Vercel detects, builds, deploys in ~30s
```

Visit `https://vercel.com/dashboard` to see deployment history.

## Troubleshooting

### "Repository not found" on push
- Check the remote URL: `git remote -v`
- Make sure you created the repo on GitHub first
- Check your token has `repo` scope

### Vercel build fails
- Lodge is pure static — no build step
- Check the deployment logs in Vercel dashboard
- Ensure `index.html` exists at the repo root

### Web Crypto not working on Vercel URL
- Vercel auto-provisions HTTPS — should work
- If you see "secure context" error, hard-refresh (Cmd+Shift+R)

### Dashboard fetches wrong config.json
- The dashboard fetches `config.json` from the **same origin**
- If dashboard is at `lodge.vercel.app/dashboard.html`, it fetches `lodge.vercel.app/config.json`
- For your real data, you need to point dashboard to YOUR server (not Vercel)
- Simplest: just use the local/Codespaces approach for real data
