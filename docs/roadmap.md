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

- [ ] Observation history and event transitions
- [ ] Offline, resource, listener, service, and SSH rules
- [ ] Deduplication, recovery, acknowledgement, and cooldown
- [ ] Webhook notification adapter

## M6 — Controlled operations

- [ ] Hub action proxy with CSRF and authorization
- [ ] Root-owned target policy and narrow privileged helper
- [ ] Start, stop, restart, and bounded log reads
- [ ] Confirmation, health verification, and complete audit records

## M7 — Declarative deployment

- [ ] Versioned stack definition and preflight
- [ ] Immutable image reference and health checks
- [ ] Backup and rollback point
- [ ] Failed-deployment rollback and audit trail
