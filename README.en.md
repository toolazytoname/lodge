<p align="center">
  <strong>Your private home base for servers and services.</strong>
  <br>
  hub + agent architecture, a single static Go binary.
</p>

<p align="center">
  <a href="README.md">中文文档</a>
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/github/license/toolazytoname/lodge?style=flat-square" alt="License"></a>
  <img src="https://img.shields.io/badge/version-2.0.0-blue?style=flat-square" alt="Version">
  <img src="https://img.shields.io/badge/stack-Go-00ADD8?style=flat-square" alt="Stack">
</p>

---

## What this is

What's running on each of your machines, and what it's exposed to (local only / tailnet only / public) — discovered automatically, not tracked in a spreadsheet that goes stale in a week.

- **agent**: runs on each managed machine under a non-root account, read-only discovery of listening ports/containers/systemd services, executes a small set of whitelisted actions via an exact-match sudoers rule — no arbitrary command execution.
- **hub**: the central node, polls each agent's state on an interval and serves a web UI; password login with login rate limiting; end-to-end encrypted vault (key derived from your master password, only ciphertext is ever stored).

## Architecture

```
Browser ──HTTPS──> lodge-hub  ──Tailscale tailnet──> lodge-agent × N
                (Go, embeds frontend)              (non-root, sudoers allowlist)
```

hub pulls (agents don't push); all machines share one tailnet. agent only binds `127.0.0.1`, exposed to the hub (or the public internet) via Tailscale serve/funnel.

## Security model

- **agent never runs as root** — a dedicated system account, never added to the `docker` group (that group is root-equivalent). Privileged operations go through an exact full-command-line allowlist in `/etc/sudoers.d/lodge-agent`; the agent invokes these with a fixed argv via `exec.Command`, never through a shell.
- **hub login**: constant-time password comparison + HMAC-signed session cookie; consecutive login failures trigger exponential-backoff lockout (per-IP bucket plus a global fallback bucket).
- **End-to-end encrypted vault**: the key is derived in the browser from your master password; the hub's database only ever holds ciphertext.

## Development

```bash
go build ./...
GOOS=linux GOARCH=amd64 go build -o dist/lodge-agent ./cmd/lodge-agent
GOOS=linux GOARCH=amd64 go build -o dist/lodge-hub ./cmd/lodge-hub

npm test    # go build + go test
```

## Layout

```
cmd/lodge-agent/    agent entrypoint
cmd/lodge-hub/      hub entrypoint
internal/agent/     discovery, service correlation, whitelisted actions
internal/hub/       API, storage, scraper, auth, login rate limiting
internal/shared/    shared types
internal/hub/web/   frontend (embedded into the hub binary)
deploy/             systemd units, sudoers template, install-agent.sh
```

## Contributing

Small, focused PRs welcome. For anything beyond a typo fix, open an issue first to align on direction. Security issues: **do not** open a public issue — email <lazywc@gmail.com>.

## License

[MIT](LICENSE)

<p align="center">
  Built by <a href="https://github.com/toolazytoname">@toolazytoname</a>
  · <a href="mailto:lazywc@gmail.com">lazywc@gmail.com</a>
</p>
