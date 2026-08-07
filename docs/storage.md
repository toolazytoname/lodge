# Durable storage

## Scope and current status

`internal/storage` provides the SQLite persistence adapter for Lodge's durable
domain model. The Hub opens `/var/lib/lodge-hub/lodge.db` by default, records
every complete, partial, and offline observation, and keeps only the latest UI
projection in memory. The embedded UI does not expose history yet; browsing and
alerting over these records belongs to M5.

The schema stores:

- Hosts, containing display metadata but no Agent URL, token, password, or SSH
  credential;
- immutable observations and their workload and endpoint children;
- annotations;
- event lifecycle records with active-event deduplication;
- operation audit records.

Schema v2 records content-addressed legacy annotation imports so restarting the
Hub cannot duplicate an import. Schema v3 adds optional Compose project/service
identity to Docker workloads; upgrading existing observations fills both fields
with empty strings without rewriting historical workload identity.

## Database invariants

- Foreign keys and WAL mode are enabled on every connection.
- Tables use SQLite `STRICT` mode and constrained booleans, ports, and foreign
  keys.
- Observation timestamps use fixed-width UTC nanoseconds so text ordering is
  chronological even when whole-second and fractional values are mixed.
- Workloads and endpoints are deleted automatically when an observation is
  pruned.
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
`0` explicitly disables automatic observation pruning. Event retention will be
defined with the event lifecycle in M5.

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
