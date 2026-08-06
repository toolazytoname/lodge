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
- **hub**: the central node, polls each Agent's state and serves a unified Web UI. It currently provides password login and login rate limiting. History, alerts, controlled operation proxying, and a vault remain roadmap items and are not security promises until implemented.

## Architecture

```
Browser ──HTTPS──> lodge-hub  ──Tailscale tailnet──> lodge-agent × N
                (Go, embeds frontend)              (non-root, sudoers allowlist)
```

The Hub pulls; Agents do not push. All machines share one tailnet. An Agent only binds `127.0.0.1` and uses Tailscale Serve to expose its API to the authorized Hub inside the tailnet. Lodge management endpoints must not use Funnel or otherwise be exposed publicly.

## Security model

- **agent never runs as root** — a dedicated system account, never added to the `docker` group (that group is root-equivalent). Privileged operations go through an exact full-command-line allowlist in `/etc/sudoers.d/lodge-agent`; the agent invokes these with a fixed argv via `exec.Command`, never through a shell.
- **Hub login**: an Argon2id verifier instead of a stored plaintext password, an independent random session-signing key, and `Secure`, `HttpOnly`, `SameSite=Strict` cookies. State-changing requests require a CSRF token. Consecutive login failures trigger exponential-backoff lockout.
- **Management boundary**: production management endpoints are tailnet-only. Authentication is defense in depth, not permission to expose the console publicly. Controlled operations and a vault remain roadmap work.

## Development

```bash
go build ./...
GOOS=linux GOARCH=amd64 go build -o dist/lodge-agent ./cmd/lodge-agent
GOOS=linux GOARCH=amd64 go build -o dist/lodge-hub ./cmd/lodge-hub

npm test    # complete local quality gate: format, vet, build, tests, race, scripts, and docs data
```

## Layout

```
cmd/lodge-agent/    agent entrypoint
cmd/lodge-hub/      hub entrypoint
internal/agent/     discovery, service correlation, whitelisted actions
internal/domain/    durable domain contracts and invariants
internal/storage/   SQLite migrations, history, and backup
internal/hub/       API, projection, scraper, auth, login rate limiting
internal/shared/    shared types
internal/hub/web/   frontend (embedded into the hub binary)
deploy/             systemd units, sudoers template, install-agent.sh
docs/               architecture, threat model, quality gates, and delivery docs
```

## Engineering documentation

- [Architecture](docs/architecture.md)
- [Threat model](docs/threat-model.md)
- [Hub authentication and migration](docs/hub-authentication.md)
- [Durable storage, migrations, and backup](docs/storage.md)
- [Tailnet-only deployment](docs/tailnet-deployment.md)
- [Quality gates](docs/quality-gates.md)
- [Development and acceptance](docs/development.md)
- [Implementation roadmap](docs/roadmap.md)

## Contributing

Small, focused PRs welcome. For anything beyond a typo fix, open an issue first to align on direction. Security issues: **do not** open a public issue — email <lazywc@gmail.com>.

## License

[MIT](LICENSE)

<p align="center">
  Built by <a href="https://github.com/toolazytoname">@toolazytoname</a>
  · <a href="mailto:lazywc@gmail.com">lazywc@gmail.com</a>
</p>
