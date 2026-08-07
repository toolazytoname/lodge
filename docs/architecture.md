# Architecture

## Context

Lodge is a private Web operations console for a small fleet of Linux servers. A browser connects to one Hub. The Hub pulls observations from a non-root Agent on each host over Tailscale.

```text
Browser --HTTPS/Tailnet--> Hub --Tailscale/grant--> Agent x N
                              |                    |-- collectors (read-only)
                              |                    `-- approved actions
                              |-- inventory/history
                              |-- event rules
                              `-- operation audit
```

The browser remains the product client. No native desktop client is required.

The browser source lives in `frontend/src` and is compiled as strict,
framework-free TypeScript into `internal/hub/web/app.js`; its source stylesheet
is copied to `internal/hub/web/app.css`. Both remain embedded in the Hub binary.
Browser request/response declarations are generated from the
exported Go HTTP contract by `cmd/lodge-web-types`; the generated declaration
and embedded JavaScript/CSS are checked for drift in every quality run. This keeps
the single-binary deployment model without maintaining a second handwritten API
model.

The browser may explicitly ask the Hub to check the current Web links. The Hub
performs bounded metadata-only `HEAD` probes from its own network perspective,
then atomically stores the latest result set. It does not proxy response data to
the browser. See [ADR 0006](adr/0006-hub-web-link-probes.md).

Historical browsing uses a separate bounded projection rather than returning
complete immutable observations. `/api/history` reads at most 500 points in one
query and exposes only timestamp, online/error state, resource percentages, and
aggregate workload/endpoint/warning counts. Full workload and route payloads
remain available to internal rule evaluation without multiplying browser data.

## Trust boundaries

1. **Browser to Hub**: human authentication, CSRF protection, output encoding, and operation confirmation.
2. **Hub to Agent**: Tailscale network identity plus a per-agent credential until application capabilities replace it.
3. **Agent to root operations**: root-owned policy and exact typed actions; no shell input and no docker group membership.
4. **Hub storage**: annotations, history, events, sessions, and audit data; never plaintext server passwords or agent credentials in API responses or logs.

## Domain model

- **Host**: one unique physical or virtual machine. Multiple SSH users do not create multiple hosts.
- **Workload**: Docker container (with optional Compose project/service identity), systemd unit, or identified process.
- **Endpoint**: a protocol/address/port plus binding and independently evidenced reachability.
- **ProxyRoute**: a redacted HTTP(S) scheme/host/port/path published by a Caddy or Nginx workload, with optional credential-free upstream authorities.
- **WebLinkCheck**: latest bounded HTTP probe evidence for one Host, Workload,
  and URL, explicitly scoped to the Hub's network perspective.
- **Observation**: an immutable collection result at a point in time.
- **Event**: one deduplicated incident derived from observations, with an
  `active` → `acknowledged` → `resolved` lifecycle. Acknowledgement records
  operator awareness; only later observation truth resolves it.
- **Operation**: a requested, authorized, executed, and audited state change.

Binding and reachability are separate. `0.0.0.0:PORT` means `wildcard-bound`;
only an external probe or authoritative firewall/provider evidence can produce
confirmed public reachability.

These contracts live in `internal/domain`. Agent `/v1` payloads are wire
contracts, not database or UI models; `internal/hub/projectObservation`
translates them and rejects duplicate identities, cross-host references,
invalid endpoints, and unevidenced reachability claims.

Event rules consume the previous observation, the current observation, and the
host's non-resolved events. Rules produce host-scoped current-truth signals;
SQLite stores the immutable observation and reconciles those signals in one
transaction. See [ADR 0007](adr/0007-observation-event-lifecycle.md).

Durable state is stored through `internal/storage` in owner-only SQLite. The
schema normalizes immutable observations, workload/endpoint/proxy-route children, annotations,
latest Web-link checks, events, and operation audit records. Agent credentials remain runtime
configuration rather than durable inventory data. See
[`docs/storage.md`](storage.md) for migration, backup, and restore invariants.

## Package direction

The target dependency direction is:

```text
transport/http -> application services -> domain
storage/sqlite -------------------------> domain
agent adapters -> collection/action contracts -> shared API
```

Domain decisions must not depend on HTTP, HTML, SQLite, or command execution. Handlers validate and translate; application services coordinate; adapters perform I/O.

## Compatibility

- Agent APIs are explicitly versioned (`/v1`).
- Browser API types are generated from the Go Hub contract and stale output
  fails the merge gate.
- Hub must report an actionable incompatibility instead of silently accepting unknown contracts.
- Database schema changes use ordered migrations and are tested from the last released schema.
- A Hub release should support at least the immediately previous Agent release during rolling upgrades.

## Decisions

Architecture decisions are recorded under [`docs/adr`](adr/). The roadmap lives in [`docs/roadmap.md`](roadmap.md).
