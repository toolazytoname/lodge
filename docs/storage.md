# Durable storage

## Scope and current status

`internal/storage` provides the SQLite persistence adapter for Lodge's durable
domain model. It is implemented and tested, but the Hub still needs to be wired
to it and the legacy JSON state needs a one-time import before M2 is complete.
Until that integration lands, production observation history is not yet a
product capability.

The first schema stores:

- Hosts, containing display metadata but no Agent URL, token, password, or SSH
  credential;
- immutable observations and their workload and endpoint children;
- annotations;
- event lifecycle records with active-event deduplication;
- operation audit records.

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

## Backup and restore procedure

The storage adapter's `Backup` method creates a consistent standalone database
with `VACUUM INTO`, refuses to overwrite an existing file, sets mode `0600`, and
runs `PRAGMA integrity_check` plus a schema-version check before reporting
success. A deployment command will expose this operation when the Hub switches
to SQLite.

Once integrated, the safe operator flow is:

1. create a timestamped backup on the same host through the Lodge backup command;
2. verify that it completed and retain the command's integrity-check evidence;
3. copy the completed standalone backup to encrypted off-host storage;
4. test restore into a separate path with the same Lodge version;
5. never copy only `lodge.db` from a running WAL database.

Restore is intentionally offline: stop the Hub, retain the current database as a
rollback point, place the verified backup at the configured database path with
owner-only permissions, then start the same or a newer compatible Hub binary.

## Retention

`PruneObservations` removes observations older than a supplied UTC cutoff in one
database operation. Retention scheduling and its production default will be
added during Hub integration; until then no automatic deletion is implied.
