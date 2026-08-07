# Threat model

## Protected assets

- ability to observe and change server workloads;
- Agent credentials and Hub sessions;
- service URLs, notes, inventory, and security events;
- operation history and deployment state;
- availability of the Hub and Agents.

## Relevant adversaries

- internet scanners and SSH/password attackers;
- a malicious Web origin targeting an authenticated browser;
- a compromised workload on one managed host;
- a compromised tailnet device that should not reach management services;
- an attacker who obtains the Hub config, legacy state, database, or backup;
- accidental operator actions.

## Required controls

### Network

- The Hub management UI is tailnet-only by default.
- Grants restrict personal devices to the Hub/SSH and restrict the Hub to Agent ports.
- Agents bind only to loopback and are never exposed through Funnel.
- Public service reachability is distinct from management reachability.

### Authentication and sessions

- Password verifiers use a memory-hard or approved slow password hash.
- Session signing uses an independent random secret.
- Cookies are `Secure`, `HttpOnly`, and appropriately `SameSite`.
- State-changing browser requests use CSRF protection.
- Login and Agent authentication are rate limited without enabling trivial fleet-wide denial of service.

### Input and output

- URLs allow only explicitly supported schemes (`https` and, where justified, `http`).
- User and Agent data is rendered without inline JavaScript or unsafe HTML interpolation.
- Request bodies, fields, command output, and log reads have size limits.
- Security headers are set by the Hub, including a restrictive Content Security Policy.

### Privileged actions

- There is no arbitrary command endpoint.
- `lodge` is not in `docker`, `sudo`, `wheel`, or equivalent groups.
- Action targets must be discovered and permitted by a root-owned policy.
- Commands use fixed argv, deadlines, bounded output, and no shell.
- Compose discovery requests only Docker's official project/service labels;
  full label maps, working directories, config-file paths, commands, and
  environments are not collected. systemd discovery emits only validated unit
  names and states; fragment paths are used in-memory solely to classify
  operator-managed units.
- The root-only process-origin collector reads only PID/UID, process and
  executable basenames, and a working-directory basename plus one-way
  fingerprint. It never reads or emits command lines, environments, or full
  paths; its self-invocation is an exact sudoers entry with no dynamic args.
- Every state-changing operation has an append-only logical audit entry.

### Secrets

- Secrets never appear in API payloads, logs, operation output, screenshots, or Git.
- Config files containing Agent credentials are mode `0600` and owned only by
  the dedicated Hub service account (or delivered as systemd credentials).
- Agent URLs and bearer tokens exist only in the private config and process
  memory. They are excluded from the SQLite schema and legacy imports, with
  regression tests scanning the database, WAL, and SHM files for sentinel
  credentials.
- A migrated JSON state file may still contain historical Agent tokens. It is
  owner-only, read only as an explicit migration source, and removed after the
  verified rollback window.
- Vault functionality is not claimed until its browser cryptography, recovery model, and compromise boundaries are independently documented and tested.

### Persistence and recovery

- SQLite database, WAL, SHM, and backup files are mode `0600`; symlinks and
  broader existing permissions are rejected.
- Ordered migrations are transactional and checksum-verified. A binary refuses
  a newer schema or a modified migration ledger.
- Backups use SQLite's consistency mechanism, never a main-file-only copy in WAL
  mode, and must pass integrity and source-version checks.
- Observation retention defaults to 30 days and runs with foreign-key cascades;
  a failed write or retention sweep is logged instead of silently ignored.

## Release review

Any change involving authentication, request routing, HTML rendering, persistence, command execution, deployment, or log collection must update this model or explicitly state why the boundaries are unchanged.
