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
- Observation-history responses are host-scoped, authenticated, aggregated,
  and capped at 500 points; they do not expose complete historical workload or
  route payloads.
- Event reads are authenticated and capped at 500 views; internal deduplication
  keys are excluded. Acknowledgement is a CSRF-protected POST and cannot alter a
  resolved incident.
- Webhook delivery uses only an owner-configured absolute HTTPS URL, disables
  environment proxies and redirects, sets a stable idempotency key, sends a
  bounded versioned JSON event view without internal dedupe keys, and never
  reads response bodies. Status and network failures are reduced to bounded
  categories before persistence or logging.
- Security headers are set by the Hub, including a restrictive Content Security Policy.
- Active Web-link checks are an authenticated, CSRF-protected management
  capability. They use only absolute credential-free http(s) targets from the
  current service view, `HEAD`, no redirects, no environment proxy, no response
  body, bounded concurrency and deadlines, and sanitized persisted errors.
  Private destinations are intentionally in scope, so this endpoint must never
  become an unauthenticated general request proxy.

### Privileged actions

- There is no arbitrary command endpoint.
- `lodge` is not in `docker`, `sudo`, `wheel`, or equivalent groups.
- Action targets must be discovered and permitted by a root-owned policy.
- Commands use fixed argv, deadlines, bounded output, and no shell.
- Sudoers contains one exact action executor, not per-target Docker/systemd
  commands. It reads only a bounded action ID from stdin and resolves it against
  a regular, non-symlink, root-owned mode `0600` policy beneath a root-owned
  non-writable directory. A missing/invalid policy fails closed; target identity
  must exactly match a narrow systemd-service or Docker-name grammar.
- Agent HTTP action requests contain no body, command, argument, or path. Both
  the non-root process and root helper re-resolve policy, serialize execution,
  bound time/output, and verify post-action state. Helper stderr and raw process
  errors never cross the HTTP boundary.
- The Hub accepts only an Agent ID, action ID, and exact confirmation phrase in
  a bounded CSRF-protected JSON request. It re-lists the live Agent policy before
  every execution, rejects unknown fields/query parameters, and serializes
  actions fleet-wide. The browser cannot submit a command, executable, target,
  argument, Agent URL, bearer credential, or expected result.
- A state-changing Agent POST is sent exactly once: redirects, environment
  proxies, and retries are disabled. Timeout or lost response is recorded as an
  uncertain categorized failure, never interpreted as permission to replay.
- Compose discovery requests only Docker's official project/service labels;
  full label maps, working directories, config-file paths, commands, and
  environments are not collected. systemd discovery emits only validated unit
  names and states; fragment paths are used in-memory solely to classify
  operator-managed units.
- Reverse-proxy discovery is one exact root-owned Agent self-invocation with no
  caller-controlled container, path, or command. It reads standard host proxy
  config and explicit Docker bind mounts selected from Docker mount metadata;
  it never reads container environment variables or runs `docker exec`. Raw
  Caddy/Nginx config, certificate/key paths, headers, authentication directives,
  URL credentials, queries, and upstream paths are never emitted. Only
  validated scheme/host/port/path plus credential-free upstream authorities are
  accepted, with time and output bounds. Host Nginx includes are followed only
  inside `/etc/nginx` through a traversal-confined root, with file-count, depth,
  regular-file, and aggregate-size limits; escaping symlinks and variable
  include paths fail closed. The exact conventional Certbot TLS-policy include
  is ignored without being read because it does not define routing.
- The root-only process-origin collector reads only PID/UID, process and
  executable basenames, and a working-directory basename plus one-way
  fingerprint. It never reads or emits command lines, environments, or full
  paths; its self-invocation is an exact sudoers entry with no dynamic args.
- The root-only SSH collector reads only an 8 MiB tail from one of two fixed,
  regular, non-symlink, non-group/world-writable authentication-log paths and
  proves that the tail covers the complete ten-minute window. If neither
  exists, it runs one fixed
  five-second journald query. Raw messages stay inside the root helper. Only
  failed password/public-key/keyboard-interactive or maximum-attempt records
  contribute; output is capped at one million failures and the top 20 canonical
  source IP/count pairs. It never emits usernames, accepted logins, ports,
  arbitrary log fields, or raw log text. The helper is an exact sudoers
  self-invocation with no dynamic args.
- The root-only security-posture collector has no caller input and runs only
  fixed local status commands. It reduces their output inside root to a closed
  enum for SSH listener/password/root/public-key posture plus UFW, Fail2Ban and
  Tailscale. It excludes users, keys, rule bodies, addresses, command output,
  cloud-edge policy and reachability claims; absent/unknown values are never
  treated as protective controls.
- Controlled log output is limited, UTF-8 normalized, stripped of control
  characters, and redacted for common credential patterns. Because arbitrary
  workload text can still be sensitive, log lines are transient authenticated
  output and are never stored in the operation audit.
- Every state-changing operation has an append-only logical audit entry.
- Audit identity is a per-login pseudonymous session fingerprint (or the
  explicit tailnet-only operator label when password authentication is
  disabled), not the reversible session cookie. Durable records contain action,
  host, target, lifecycle timestamps, bounded summary, and error category; they
  exclude Agent credentials, commands, raw errors, and log lines.
- Deployment policy is a regular, non-symlink, root-owned mode `0600` file and
  accepts only explicitly stateless Compose services, immutable sha256 image
  references, and Docker or exact loopback HTTP health checks. Compose project,
  file, optional `.env`, and their full path chain must be root-owned and not
  group/world writable; the browser and Hub cannot submit any of them.
- The deployment helper is one exact argv with a bounded stdin ID. It uses fixed
  Docker/Compose argv without a shell, changes only the registered service with
  `--no-deps`, bounds command output/time, and never emits Compose/environment,
  Docker output, health response, or raw errors. It shares the action lock.
- Current/previous immutable release and generated override are one canonical
  root-only file. A mismatch fails closed. Candidate state is synced and renamed
  only after health succeeds; candidate failure reapplies and verifies the
  pre-operation image. Failed recovery is explicitly `rollback_failed` and is
  never hidden or automatically replayed. Stateful deployment remains disabled
  until a dedicated backup/restore adapter is designed and tested.
- Hub deployment execution re-lists Agent authority, compares the exact
  confirmation, shares the fleet-wide action lock, writes `requested` and
  `running` before returning HTTP 202, and sends one non-retried POST. Browser
  disconnect does not cancel accepted work; Hub restart marks unfinished audit
  as uncertain and never replays it. The Web receives neither the host policy
  paths nor the metadata-only Agent execution result.

### Secrets

- Lodge-managed secrets never appear in API payloads, service logs, durable
  operation audit, screenshots, or Git. Approved workload log reads may contain
  application-authored sensitive text despite defense-in-depth redaction; they
  are explicitly sensitive, authenticated, bounded, and non-durable.
- Config files containing Agent credentials are mode `0600` and owned only by
  the dedicated Hub service account (or delivered as systemd credentials).
- Agent URLs and bearer tokens exist only in the private config and process
  memory. They are excluded from the SQLite schema and legacy imports, with
  regression tests scanning the database, WAL, and SHM files for sentinel
  credentials.
- The optional Webhook bearer value is read from a separate owner-only regular
  file; symlinks, non-visible/whitespace bytes, and oversized values are
  rejected. The Webhook URL and secret never enter SQLite or delivery logs.
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
- Latest Web-link evidence is replaced atomically and never changes immutable
  observation history. A reachable result means only that the Hub received an
  HTTP response, not that the service is publicly reachable or semantically healthy.
- Event signals are validated, host-scoped, deduplicated, and reconciled in the
  same transaction as their observation. Offline and category-specific missing
  telemetry preserve existing risk rather than manufacturing recovery; stale
  event time is rejected and rolls back the observation.
- SSH summaries are bounded, clock-checked, canonicalized, and persisted only
  for online observations. Missing/invalid SSH telemetry cannot manufacture a
  recovery. Source IPs are sensitive operational data: they are exposed only
  through authenticated events, retained with event history, and may also be
  sent to the explicitly configured Webhook receiver.
- The notification outbox is atomic with event transitions, unique per
  event/transition/channel, crash-reclaimable, and bounded to eight attempts.
  Retry state stores only sanitized categories. At-least-once delivery means
  receivers must deduplicate the `X-Lodge-Delivery` key.
- Operation state transitions use compare-and-set persistence. Startup converts
  interrupted `requested`/`running` rows to `failed/hub_restarted` and never
  retries the remote action. Remote execution has its own deadline inside a
  larger persistence budget, preserving time to durably finalize a timeout.

## Release review

Any change involving authentication, request routing, HTML rendering, persistence, command execution, deployment, or log collection must update this model or explicitly state why the boundaries are unchanged.
