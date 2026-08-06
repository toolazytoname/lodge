# ADR 0004: Durable domain contracts before storage and UI

- Status: accepted
- Date: 2026-08-07

## Decision

Define Host, Workload, Endpoint, Observation, Event, and Operation in a pure
`internal/domain` package. Agent v1 payloads are translated at an explicit Hub
projection boundary. SQLite rows and future API view models depend on the domain
instead of treating the current Agent JSON as the product model.

Socket binding and reachability are separate fields. A wildcard bind projects
to `BindingWildcard` with `ReachabilityUnknown`. Public or tailnet reachability
is invalid unless it includes an evidence source and check time.

Host and workload identity is compound: a Host has one stable ID, and a
Workload key is stable only within that Host. SSH usernames never create extra
Hosts. Endpoints reference their host-scoped Workload key.

## Consequences

- The old Agent `ExposurePublic` wire value remains compatible but is treated
  only as a legacy wildcard-binding hint.
- External probe, firewall, and cloud-provider evidence can be added later
  without rewriting workload discovery.
- Persistence migrations can reject cross-host references, duplicate durable
  identities, and unsupported enum values before corrupting history.
- UI labels will migrate from the ambiguous `public` value to independent
  binding and confirmed-reachability fields.
