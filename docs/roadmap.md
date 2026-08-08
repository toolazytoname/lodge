# Implementation roadmap

The detailed findings are archived in the Wiki. This file is the normative engineering sequence used for delivery.

## M0 — Engineering baseline

- [x] Quantified scorecard and definition of done
- [x] Architecture and threat-model documents
- [x] Local deterministic quality gate
- [x] GitHub CI on a supported Go toolchain
- [x] Vulnerability scanning
- [x] Verify the first pushed CI run

## M1 — Hub security

- [x] Tailnet-only management deployment procedure
- [x] Slow password verifier and independent session secret
- [x] Secure session cookie and CSRF protection
- [x] Strict URL and annotation validation
- [x] Safe DOM rendering and restrictive security headers
- [x] CSRF, URL, injection, static-asset, and header regression tests

## M2 — Durable domain model

- [x] Host, Workload, Endpoint, Observation, Event, and Operation contracts
- [x] SQLite store with ordered migrations and backup procedure
- [x] Binding versus confirmed reachability model
- [x] Compatibility policy and migration tests

## M3 — Complete inventory

- [x] Add tencent and banwagong Agents
- [x] Docker containers and listening processes discovered on all five hosts
- [x] Privacy-preserving custom-process attribution
- [x] Compose and complete systemd/failed-unit attribution
- [x] Caddy/Nginx route discovery with redaction (11 live routes, Agent 0.4.1)
- [x] Reach 5/5 configured and online managed hosts
- [x] Reach at least 95% known-workload attribution (current live result: 100.0%)

## M4 — Product UI

- [x] TypeScript frontend with generated API types
- [x] Overview, Hosts, Services, Security, and Operations navigation and page boundaries
- [x] Responsive, accessible loading/empty/offline/error states
- [x] End-to-end and visual regression tests
- [x] Active reachability measurement for registered Web links

Live acceptance on 2026-08-08 measured 6 reachable, 1 degraded, and 4
unreachable among 11 discovered proxy routes (54.5% reachable). The M4 product
capability is complete; the fleet remains below the 95% operational target and
the failed DNS/TLS/route evidence must stay visible until each service owner
repairs or retires it.

## M5 — History and alerts

- [x] Observation history and event transitions
- [x] Offline, resource, listener, service, and SSH rules
- [x] Deduplication, recovery, acknowledgement, and cooldown
- [x] Webhook notification adapter

Live acceptance on 2026-08-08 upgraded the Hub to 0.6.0/schema 7 and all five
Agents to 0.5.1. The latest fleet projection was 5/5 online, 55 workloads, 86
endpoints, 11 routes, zero warnings, and zero unidentified workloads. A natural
bytebunny spike opened one critical SSH event 27.9 seconds after deployment
began; bytedragon also had one active warning. Real source IPs remain inside the
authenticated event view. Webhook delivery is implemented and tested but the
production receiver is intentionally disabled until one is configured.

## M6 — Controlled operations

- [x] Hub action proxy with CSRF and authorization
- [x] Root-owned target policy and narrow privileged helper
- [x] Agent start, stop, restart, health verification, and bounded log reads
- [x] Confirmation, health verification, and complete audit records

The Agent boundary is implemented in version 0.6.0: a missing policy disables
all actions, direct legacy sudo writes are removed, and only a bounded action ID
can reach the root helper. Hub 0.7.0 re-lists live Agent authority, requires the
exact phrase, sends one non-retried POST, and records a compare-and-set lifecycle
with pseudonymous requester identity and sanitized result. The responsive
Operations page covers policy, risk, Agent sync, confirmation, transient logs,
and durable audit.

Live acceptance on 2026-08-08 rolled Agent 0.6.0 to all five hosts and Hub 0.7.0
through rollback-protected installers after both GitHub CI runs passed. The
fleet remained 5/5 online with 55 workloads, 86 endpoints, 11 routes, zero
warnings, and zero unidentified workloads. A Caddy log read exceeded the 64 KiB
bound and correctly became `failed/log_read_failed` without retry; an idempotent
start of the already-running Caddy succeeded with `running → running`. A Redis
log read returned 200 transient lines, and an in-memory sample was absent from
SQLite, WAL, and SHM. All three audits reached a terminal state with no
interrupted operation. M6 is complete.

## M7 — Declarative deployment

- [x] Versioned stack definition and Agent-side preflight
- [x] Immutable image reference and health checks
- [x] Persistent immutable rollback point
- [x] Failed-deployment rollback and audit trail

Agent `0.7.0` now implements the host transaction: missing root policy disables
all deployments, version 1 rejects stateful stacks and mutable tags, Compose
paths must be root-owned, and fixed no-shell execution captures a persistent
immutable rollback point. Candidate failure reapplies and verifies the previous
image. Hub `0.8.0` re-lists live release authority, admits only one fleet-wide
action/deployment, returns HTTP 202 after durable `requested`/`running`, and
performs one non-retried background Agent call. The Operations page shows the
fixed digest and current/previous releases, requires exact confirmation, and
polls durable `succeeded`/`rolled_back`/`failed` audit. Automated implementation
acceptance is complete. Production M7 acceptance remains open until Agent
`0.7.0` and Hub `0.8.0` are rolled out fail-closed, and a first real stateless
stack plus immutable digest is explicitly reviewed; Lodge will not guess that
business target.

The fail-closed platform rollout completed on 2026-08-08 after push CI
`31221180729` and PR CI `31221182851` passed the full quality gate and
vulnerability scan. All five Agents run the same 0.7.0 artifact with existing
22 action capabilities preserved, zero deployment capabilities, absent policy,
and empty root-only rollback state. Hub 0.8.0 is live on schema 7 with a verified
rollback bundle and post-deploy backup; the fleet remains 5/5 online, 55
workloads, 86 endpoints, 11 routes, zero warnings, and zero unidentified
workloads. M7 production execution remains deliberately open until an operator
approves one real stateless stack, immutable digest, health check, and recovery
plan.

A read-only eligibility audit found three current Compose services and zero safe
version-1 targets. PostgreSQL and Redis are stateful by definition. The
application service has Docker health and an immutable repository digest, but
also has writable `/data` and `/app/logs` bind mounts, so Lodge must treat it as
stateful until its owner proves otherwise and defines workload-specific
recovery. The remaining live acceptance therefore requires either an explicitly
approved isolated stateless canary or a reviewed application refactor; it must
not be forced onto the current stack.

## M8 — SSH access hardening

- [x] Read-only five-host effective SSH, listener, firewall, Fail2Ban, and Tailscale baseline
- [x] Lockout-safe per-host rollout and acceptance runbook
- [x] Current privacy-minimized SSH/firewall/Fail2Ban/Tailnet posture in the Security page
- [x] Ali pilot: non-root Tailnet key account, OpenSSH password/root closure, and post-change rejection tests
- [x] bytebunny pilot: non-root Tailnet key account and OpenSSH password/root closure, including cloud-init ordering correction
- [x] Tailnet SSH policy: deny root and retain only `lodge-admin`, verified against both pilots
- [x] bytedragon rollout: non-root Tailnet key account, cloud-init-safe OpenSSH closure, and post-change rejection tests
- [ ] Verify a dedicated non-root Tailnet key administrator and recovery path for each host
- [ ] Disable SSH password, keyboard-interactive, and root remote login one host at a time
- [ ] Remove or allowlist public port 22 at the cloud edge; verify a host firewall and Fail2Ban posture where appropriate

The 2026-08-08 baseline found every host still accepts password authentication and root remote login on a wildcard SSH listener. bytebunny, bytedragon, and Ali additionally have no active UFW or Fail2Ban layer; tencent has both; banwagong has active UFW but no active Fail2Ban. Tailscale is running on all five, but it does not by itself close public SSH. The detailed evidence and non-lockout sequence are in [`ssh-hardening.md`](ssh-hardening.md). Ali and bytebunny subsequently completed the approved OpenSSH-side pilots; the remaining three hosts retain the baseline until their own recovery and administrator gates are verified.

The visibility slice is now live: after passing push CI `31224459335` and PR CI
`31224462704`, all five Agents run `0.8.0` and the Hub runs `0.9.0`. Each Agent
returned all seven closed-enum posture fields through its service account and
rejected an attempted extra helper argument. The Hub upgrade verified schema 7,
5 configured hosts, 14,154 observations, a rollback bundle and post-deploy
backup, unauthenticated API rejection, loopback-only binding, Tailnet Serve, and
absence of configured secret values in recent service logs. A post-rollout
Hub-local authenticated pull also validated the current seven-field posture
from all 5/5 registered Agents without exposing tokens, addresses, or raw host
data. This makes the
current posture visible; it does not change the unresolved SSH access risk.

Ali and bytebunny now have effective OpenSSH password, keyboard-interactive, and
root login disabled, with a fresh `lodge-admin` Tailnet session and public root
and password-only rejection checks recorded. bytebunny required an ordering
correction because cloud-init's `50-cloud-init.conf` set the first effective
password-authentication value; the Lodge drop-in now precedes it. The approved
Tailnet SSH policy was then changed from `autogroup:nonroot, root` to the exact
`lodge-admin` local user while retaining check mode. Fresh tests reject a
Tailnet `root` request on both pilots and still admit `lodge-admin`, closing the
previously independent root-access path. bytedragon then completed the same
cloud-init-safe rollout and public root-key/password rejection tests. Two hosts
remain before the per-host access closure is complete.
