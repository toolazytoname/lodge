# ADR 0005: SQLite for durable single-Hub state

- Status: accepted
- Date: 2026-08-07

## Decision

Persist Lodge inventory, observation history, annotations, events, and operation
audit records in one SQLite database owned by the Hub service account. Use the
CGo-free `modernc.org/sqlite` driver so the existing static Linux build and
cross-compilation workflow remain available.

Schema changes are forward-only, ordered migrations. Every applied migration is
recorded with its name and SHA-256 checksum, while `PRAGMA user_version` records
the current schema. Startup fails if the database is newer than the binary or
if the migration ledger was changed after application.

Use WAL mode for runtime access. Backups must use SQLite's `VACUUM INTO` through
the storage adapter and pass an integrity and schema-version check; copying only
the main database file while WAL is active is not an accepted backup method.

Agent URLs and bearer tokens remain in owner-private runtime configuration and
process memory. They are deliberately absent from the durable Host schema.

## Consequences

- One local database is operationally appropriate for the current single-Hub,
  five-host control plane and avoids introducing another network service.
- The database, WAL, SHM, and backup files are required to use mode `0600`.
- Write concurrency is intentionally serialized through one database connection;
  this can be revisited only after measured load requires it.
- Moving to another database later requires a new storage adapter and data
  migration, but does not change domain contracts.
- The Hub uses SQLite for every observation and annotation write. Its former
  JSON state is accepted only as an owner-only, content-addressed annotation
  import; embedded Agent connection records are ignored and must be removed
  after the verified rollback window.
- Persistence code and CI readiness do not prove that the live Hub has been
  migrated; deployment evidence remains a separate operational transaction.
