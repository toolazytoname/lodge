# Webhook event notifications

## Capability and delivery contract

The Hub can send event-open and event-recovery notifications to one
owner-configured HTTPS endpoint. It uses a durable SQLite outbox, so receiver
downtime or a Hub restart does not lose a committed transition. Delivery is
at-least-once: the receiver must deduplicate the stable `X-Lodge-Delivery`
header. The same value is also present as `deliveryKey` in the JSON body.

Payload version 1 contains:

- `transition`: `opened` or `recovered`;
- event ID, host ID, kind, severity, lifecycle state, title and bounded detail;
- first/last observation time plus acknowledgement/recovery time when present.

It deliberately excludes the internal event deduplication key, Agent
credentials, Hub session data, Webhook secret, raw observation payloads, and
network diagnostics.

## Configuration

Add the optional `webhook` object to the owner-only Hub config:

```json
{
  "passwordHash": "$argon2id$...",
  "webhook": {
    "url": "https://hooks.example.com/lodge",
    "secretFile": "/etc/lodge-hub/webhook-secret",
    "cooldownSeconds": 900
  },
  "agents": []
}
```

The URL must be absolute HTTPS without URL credentials or a fragment. Paths and
queries are allowed because providers commonly encode routing identity there;
Lodge never writes or logs the URL. The optional secret becomes an
`Authorization: Bearer ...` header. Create it as a regular, non-symlink file
owned by the Hub service account with mode `0600`; its value must be 1–4096
visible ASCII bytes without whitespace.

`cooldownSeconds` defaults to 900 and accepts 30–86400. It delays only a new
incident with the same risk key after a previously delivered open. It does not
hide the event from the Web console or mutate acknowledgement/recovery truth.

After changing configuration, restart the Hub and verify the startup line says
`Webhook 开启`; the endpoint itself is intentionally not printed.

## Receiver behavior

Return any 2xx after durably accepting the delivery. Lodge retries 408, 425,
429, 5xx, timeouts, and network failures with delays from five seconds to one
hour, up to eight attempts. Other statuses fail permanently. Redirects are not
followed, response bodies are not consumed, and proxy environment variables are
ignored.

Persist `X-Lodge-Delivery` before applying side effects. If the same key arrives
again, return 2xx without sending a duplicate message. Do not use event title or
time as an idempotency key.

## Disable and recovery

Remove the `webhook` object and restart the Hub to stop creating and delivering
Webhook rows. Existing rows remain durable operational evidence. Re-enabling
the same channel resumes due pending rows. A future maintenance command may
provide explicit failed-row replay; editing SQLite by hand is unsupported.
