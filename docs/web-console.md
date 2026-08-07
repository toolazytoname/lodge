# Web console

## Product boundaries

The embedded Web console is the primary Lodge client. Its five page boundaries
are deliberate:

- **Overview** presents current fleet health, attention signals, discovered Web
  links, and a compact host summary.
- **Hosts** presents online state, resource pressure, workload counts, Agent
  versions, and last observation times.
- **Services** is the searchable and filterable workload directory. Annotation
  editing supports an alias, a safe http(s) URL, notes, and a visual hidden flag.
- **Security** reports the observed public surface, unidentified workloads,
  offline nodes, and a host-scoped recent 120-point trend from durable observation
  summaries. Its event center shows current and recovered incidents, filters by
  host/lifecycle, and performs explicit acknowledgement. Historical SSH/login
  sources and notification delivery remain M5.
- **Operations** currently reports only read-only synchronization state. Any
  restart, deployment, or rollback remains M6/M7 and must use typed policy,
  confirmation, verification, and audit records.

The UI must not describe a discovered link as reachable until an active probe
has supplied evidence. It must not turn a failed API request into a misleading
zero count. Empty inventory, loading, offline, partial failure, and total failure
are separate product states.

## Active Web-link checks

`检查入口` is an explicit authenticated action, not a background availability
monitor. It asks the Hub to check the de-duplicated URLs currently displayed
from operator annotations, redacted proxy routes, and conservative Web-port
inference. Results are labelled **Hub 可达**, **HTTP 5xx**, or **Hub 不可达**;
links without evidence remain **未检查**.

The status is deliberately scoped to the Hub's network view:

- HTTP 100–499 is reachable because an HTTP endpoint answered, including login
  pages, redirects, and authorization failures;
- HTTP 500–599 is degraded;
- DNS, connection, TLS, and timeout failures are unreachable;
- the check does not follow redirects or prove Internet/public reachability,
  application correctness, or browser-side reachability.

Only the latest bounded metadata is retained in SQLite. Raw network errors,
response bodies, headers, resolved addresses, and credentials are excluded.

## Event API boundary

`GET /api/events` returns at most 500 event views, globally or scoped by the
validated `agent` query. Views include incident type, severity, lifecycle state,
operator-facing detail, and audit timestamps; the internal deduplication key is
not exposed. `POST /api/events/ack?id=...` requires an authenticated session and
CSRF token. It is idempotent for an acknowledged event, returns not found for an
unknown ID, and refuses to rewrite a resolved incident.

The Security event center defaults to ongoing incidents and keeps acknowledged
risk visible until recovery. It shows host, kind, severity, duration, last
observation, and lifecycle state; resolved history remains available by filter.
Event API failure is isolated from current surface and history data. This does
not yet claim notification delivery, SSH-origin events, or cooldown.

## Source and embedded assets

`frontend/src/app.ts` and `frontend/src/app.css` are the editable sources.
`npm run build:web` generates the Go-derived TypeScript contract, compiles the
browser module, and installs both embedded assets under `internal/hub/web`.
`npm run check:web` fails if generated types, JavaScript, or CSS drift.

The browser has no third-party runtime dependency. TypeScript and Playwright are
development-only dependencies pinned by `package-lock.json`.

## Browser acceptance

Install the test browser once after `npm ci`:

```bash
npx playwright install chromium
```

Run the responsive and state matrix:

```bash
npm run test:web:e2e
```

The deterministic fixture server uses only invented host names and
`fixture.example.test` URLs. It covers:

- normal 5-host / 55-service density at 390, 1280, and 1920 pixels;
- empty inventory;
- one offline node;
- a partial services API failure with usable host data;
- a total API failure with explicit unavailable values;
- service search, risk ordering, dialog focus, and URL protocol rejection;
- persisted link status plus the CSRF-protected active-check interaction;
- host-scoped 120-point history, host switching, responsive trend charts, and
  an isolated history-API error that preserves current inventory.
- event rendering, host/lifecycle filters, CSRF-protected acknowledgement,
  retained acknowledged risk, and an isolated event-API error.

Reference screenshots live in `frontend/tests/__screenshots__`. Time, locale,
timezone, data, and viewport are fixed. Update screenshots only for an intended
design change, inspect every resulting image, then run the test again without
the update flag:

```bash
npx playwright test --update-snapshots=all
npm run test:web:e2e
```

CI installs Chromium, repeats the complete matrix, and uploads traces,
screenshots, and diffs for seven days when a browser gate fails.
