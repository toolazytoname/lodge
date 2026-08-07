# ADR 0007: Observation-derived event lifecycle

- Status: accepted
- Date: 2026-08-08

## Context

Lodge samples every host repeatedly. Writing one alert row per sample would
create noise, hide duration, and make acknowledgement meaningless. Conversely,
resolving an event whenever a collector omits data would turn telemetry loss
into false recovery. A first deployment also already contains wildcard-bound
listeners, so comparing it with an empty database would label the entire fleet
as newly exposed.

## Decision

Rules emit current `EventSignal` truth with a host-scoped deduplication key.
SQLite reconciles that set against non-resolved events:

- a new key opens one `active` event;
- a continuing key updates severity, detail, and `last_observed_at` in place;
- acknowledgement changes `active` to `acknowledged` without implying recovery;
- a missing key changes the event to `resolved` and records `resolved_at`;
- a later recurrence opens a new event and preserves the resolved incident.

The immutable observation and all event changes commit in the same SQLite
transaction. Duplicate, cross-host, invalid, or chronologically stale signals
roll back the complete write.

Offline observations carry existing workload, listener, and resource signals
because absent telemetry is not recovery. Online partial observations carry
only the categories whose collector data is missing. The first online listener
set, and the first set after an offline interval, establish a baseline; only a
new wildcard listener seen between two complete online service collections
opens an event. Existing listener events remain active until that listener is
observed absent.

Resource rules use hysteresis: memory opens at 85% and clears below 80%, root
disk opens at 90% and clears below 85%, and one-minute load per CPU opens at
1.5 and clears below 1.0. Failed or unhealthy workloads are critical. Host
offline is critical. Notification cooldown and delivery are separate from this
source-of-truth lifecycle.

## Consequences

- Operators see incidents rather than one row per polling interval.
- Acknowledgement records awareness without muting ongoing truth.
- Missing telemetry errs toward preserving risk and records host collection
  failure separately.
- Listener rollout avoids a fleet-wide false-positive burst, at the cost of
  treating pre-existing wildcard bindings as baseline inventory.
- The storage adapter, rule evaluator, API, UI, and notification adapter can be
  tested independently against one explicit state machine.
