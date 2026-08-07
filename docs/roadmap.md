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
- [ ] Failed-deployment rollback and audit trail

Agent `0.7.0` now implements the host transaction: missing root policy disables
all deployments, version 1 rejects stateful stacks and mutable tags, Compose
paths must be root-owned, and fixed no-shell execution captures a persistent
immutable rollback point. Candidate failure reapplies and verifies the previous
image. M7 remains incomplete until the Hub performs asynchronous, non-retried
execution with durable `succeeded`/`rolled_back`/`failed` audit and the Web UI
requires exact confirmation.
