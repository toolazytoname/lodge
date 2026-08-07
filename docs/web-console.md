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
- **Security** currently reports only the observed public surface, unidentified
  workloads, and offline nodes. Historical login sources and alerts remain M5.
- **Operations** currently reports only read-only synchronization state. Any
  restart, deployment, or rollback remains M6/M7 and must use typed policy,
  confirmation, verification, and audit records.

The UI must not describe a discovered link as reachable until an active probe
has supplied evidence. It must not turn a failed API request into a misleading
zero count. Empty inventory, loading, offline, partial failure, and total failure
are separate product states.

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
- service search, risk ordering, dialog focus, and URL protocol rejection.

Reference screenshots live in `frontend/tests/__screenshots__`. Time, locale,
timezone, data, and viewport are fixed. Update screenshots only for an intended
design change, inspect every resulting image, then run the test again without
the update flag:

```bash
npx playwright test --update-snapshots
npm run test:web:e2e
```

CI installs Chromium, repeats the complete matrix, and uploads traces,
screenshots, and diffs for seven days when a browser gate fails.
