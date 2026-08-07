# ADR 0008: Durable at-least-once Webhook notifications

- Status: accepted
- Date: 2026-08-08

## Context

Sending HTTP directly from the scrape transaction couples fleet observation to
an external service. A timeout would slow collection, a process crash could lose
an event, and retrying an entire observation could duplicate lifecycle changes.
An in-memory queue would still lose work on restart. Notification noise also
needs a separate recurrence cooldown without changing event truth.

## Decision

Every configured event `opened` or `recovered` transition creates a unique
SQLite outbox row in the same transaction as its Observation and Event. Network
delivery is performed after commit by a separate single-channel worker.

The worker leases one due row before HTTP I/O. An expired `sending` lease can be
reclaimed after a crash, so delivery is at-least-once rather than exactly-once.
Each request carries `X-Lodge-Delivery: <event-id>:<transition>:webhook`; the
receiver must use it as an idempotency key. Success is any 2xx. HTTP 408, 425,
429, 5xx, timeout, and network failures retry with bounded backoff; other HTTP
statuses stop immediately, and all deliveries stop after eight attempts.

The Webhook URL must be absolute HTTPS and cannot contain userinfo or a fragment.
Redirects and environment proxies are disabled. An optional bearer secret comes
from a separate owner-only, non-symlink file. Payloads use an explicit versioned
event projection and omit the internal deduplication key. Response bodies and
raw transport errors are never read into storage or logs.

Cooldown applies only to a later event with the same rule deduplication key. Its
opened notification is delayed from the prior successful open delivery. If the
new incident recovers while still pending, the stale open is cancelled and no
unseen recovery is sent. If an open was already leased or delivered, its
recovery is queued.

## Consequences

- Observation collection remains independent of receiver availability.
- A committed transition cannot silently disappear on Hub restart.
- The receiver must implement idempotency because a crash between HTTP success
  and local completion can produce a duplicate.
- Terminal delivery rows remain as bounded operational evidence in SQLite;
  the Webhook URL, secret, response, and raw error do not.
- Adding another notification channel requires a new adapter and channel policy,
  not changes to event lifecycle truth.
