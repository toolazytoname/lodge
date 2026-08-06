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
- [ ] SQLite store with ordered migrations and backup procedure
- [x] Binding versus confirmed reachability model
- [ ] Compatibility policy and migration tests

## M3 — Complete inventory

- [ ] Add tencent and banwagong Agents
- [ ] Docker/Compose/systemd/custom-process discovery
- [ ] Caddy/Nginx route discovery with redaction
- [ ] Reach 5/5 managed hosts and at least 95% known-workload attribution

## M4 — Product UI

- [ ] TypeScript frontend with generated API types
- [ ] Overview, Hosts, Services, Security, and Operations pages
- [ ] Responsive, accessible loading/empty/offline/error states
- [ ] End-to-end and visual regression tests

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
