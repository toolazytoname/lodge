# Durable storage

## Scope and current status

`internal/storage` provides the SQLite persistence adapter for Lodge's durable
domain model. The Hub opens `/var/lib/lodge-hub/lodge.db` by default, records
every complete, partial, and offline observation, and keeps only the latest UI
projection in memory. M5 exposes bounded history and event APIs in the Security
page and persists the lifecycle, reliable notification delivery state, and
privacy-minimized SSH authentication-failure summaries.

The schema stores:

- Hosts, containing display metadata but no Agent URL, token, password, or SSH
  credential;
- immutable observations and their workload, endpoint, and redacted proxy-route children;
- annotations;
- the latest bounded Web-link probe result set;
- event lifecycle records with active-event deduplication;
- notification outbox rows for configured event channels;
- operation audit records.

Schema v2 records content-addressed legacy annotation imports so restarting the
Hub cannot duplicate an import. Schema v3 adds optional Compose project/service
identity to Docker workloads; upgrading existing observations fills both fields
with empty strings without rewriting historical workload identity.
Schema v4 adds normalized Caddy/Nginx proxy routes. It stores only scheme,
validated host, port, path matcher, and credential-free upstream authorities;
raw configuration, certificate/key paths, headers, query strings, and
credentials never enter SQLite.
Schema v5 adds the latest Hub-scoped Web-link checks, keyed by host, workload,
and URL. Each row contains only state, HTTP status when present, latency,
sanitized error category, and check time. Replacing a probe run is atomic; raw
errors, response headers/bodies, resolved addresses, and credentials are never
stored.
Schema v6 adds the event notification outbox. The outbox contains the event ID,
transition, channel, sanitized delivery state/error kind, attempt count, and
delivery schedule. It does not contain the Webhook URL, bearer secret, response
body, response headers, or raw network errors.
Schema v7 adds an optional JSON SSH failure summary to each immutable
Observation. The validated document contains a 5–15 minute UTC window, total
failure count, and at most 20 canonical source IP/count pairs. Usernames,
destination/source ports, accepted logins, authentication-log fields, and raw
messages are not stored.

## Database invariants

- Foreign keys and WAL mode are enabled on every connection.
- Tables use SQLite `STRICT` mode and constrained booleans, ports, and foreign
  keys.
- Observation timestamps use fixed-width UTC nanoseconds so text ordering is
  chronological even when whole-second and fractional values are mixed.
- Workloads, endpoints, and proxy routes are deleted automatically when an observation is
  pruned.
- Timeline reads use one bounded summary query (default 120, maximum 500
  points). They do not materialize every historical workload, endpoint, route,
  or full resource payload into the browser response.
- Each deduplication key has at most one non-resolved event. Continuing signals
  update that row; recovery closes it; recurrence creates a new historical row.
- An observation and its event opens, updates, and recoveries commit in one
  transaction. Invalid, duplicate, cross-host, or stale signals roll back both.
- Acknowledgement is idempotent and preserves active risk until a later
  observation resolves it.
- Event transitions and their configured notification rows commit in the same
  transaction. A channel gets at most one row per event transition.
- A due row is claimed with a renewable crash-recovery lease. A worker crash or
  completion-write failure can cause a duplicate network delivery, so every
  request carries a stable receiver-side idempotency key.
- A recurrence of the same risk is delayed by the configured cooldown. If it
  recovers before that delayed open is claimed, the stale open is cancelled and
  no recovery notification is fabricated.
- SSH summaries must be from an online observation, use canonical unique IPs,
  remain within five minutes of Hub observation time, and have bounded,
  internally consistent counts. Invalid Agent input becomes an explicit
  partial-telemetry warning rather than an event.
- The database, `-wal`, `-shm`, and backup files are owner-only (`0600`). An
  existing database with broader permissions or a symlinked path is rejected.

## Migrations and compatibility

Migrations live in `internal/storage/migrations.go` and are applied in increasing
version order in a transaction. Never edit an applied migration. Add a new
numbered migration and a test that upgrades from the previous released schema.

Startup verifies both `PRAGMA user_version` and the migration ledger checksum.
It refuses a database created by a newer Lodge binary rather than attempting a
destructive downgrade.

The Hub accepts the Agent API version declared by `shared.APIVersion` only.
Missing or unknown versions are stored as an explicit offline observation and
no further endpoints are requested. When a new Agent API is introduced, the Hub
must explicitly retain the immediately previous version for the documented
rolling-upgrade window rather than accepting unknown payloads.

## Legacy JSON migration

`--state /etc/lodge-hub/state.json` is now a read-only migration source. If the
owner-only file exists, Lodge hashes its complete contents and atomically imports
its annotations once. Agent URLs and tokens in the old `agents` array are never
decoded into runtime configuration or written to SQLite. Annotations for hosts
not present in the current private config are counted and skipped.

Startup rejects a symlink, a file larger than 4 MiB, group/world permissions,
unknown JSON fields, unsafe URLs, and invalid durable identities. Existing
SQLite annotations win over imported values. After verifying the imported
annotations and keeping a separate rollback artifact, remove the legacy file;
it may still contain old Agent tokens.

## Backup and restore procedure

The `--backup` command creates a consistent standalone database with `VACUUM
INTO`, refuses to overwrite an existing file, sets mode `0600`, and runs
`PRAGMA integrity_check` plus a source/backup schema-version comparison before
reporting success. It requires an existing initialized source and does not run
migrations, so it is safe as a pre-upgrade rollback point.

```bash
sudo install -d -o lodge -g lodge -m 0700 /var/lib/lodge-hub/backups
sudo -u lodge /usr/local/bin/lodge-hub \
  --database /var/lib/lodge-hub/lodge.db \
  --backup "/var/lib/lodge-hub/backups/lodge-$(date -u +%Y%m%dT%H%M%SZ).db"
```

The safe operator flow is:

1. create a timestamped backup on the same host through the Lodge backup command;
2. verify that it completed and retain the command's integrity-check evidence;
3. copy the completed standalone backup to encrypted off-host storage;
4. test restore into a separate path with the same Lodge version;
5. never copy only `lodge.db` from a running WAL database.

Restore is intentionally offline: stop the Hub, retain the current database as a
rollback point, place the verified backup at the configured database path with
owner-only permissions, then start the same or a newer compatible Hub binary.
Restoring a database created by a newer schema into an older binary is rejected.

## Retention

`PruneObservations` removes observations older than a supplied UTC cutoff in one
database operation with workload/endpoint cascade. The Hub runs one sweep at
startup and every six hours. `--history-retention` defaults to `720h` (30 days);
`0` explicitly disables automatic observation pruning. Events are currently
retained as incident history; a separate bounded retention policy must be
defined before fleet scale makes indefinite retention inappropriate.

## First production migration

Repository readiness does not prove that the live Hub has migrated. Roll out one
Hub at a time:

1. keep a second SSH session and verify the Tailnet-only recovery path;
2. stop the Hub and retain an owner-only rollback copy of the old binary,
   systemd unit, config, and `state.json`;
3. make `config.json` and `state.json` mode `0600` before starting the new binary;
4. install the new unit, which creates `/var/lib/lodge-hub` with mode `0700`;
5. start the Hub and confirm the migration log, a successful Agent scrape, UI
   annotations, and a successful `--backup` command;
6. verify the Tailnet URL and then remove the legacy state and its rollback copy
   after the rollback window closes.

`deploy/install-hub.sh` implements this as a single-host transaction. It takes
an expected artifact SHA-256, stops only the Hub, creates an owner-only rollback
bundle, runs the in-process password migration, installs the binary and unit,
then verifies loopback HTTP, protected file ownership, SQLite integrity/schema,
credential-free Host columns, and a live post-deploy backup. Its `EXIT` trap
restores the old binary, unit, config, optional state/session, and database when
any acceptance check fails.

```bash
sudo deploy/install-hub.sh apply /tmp/lodge-hub <expected-sha256>
```
