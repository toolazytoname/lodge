# ADR 0006: Hub-scoped active Web link checks

- Status: accepted
- Date: 2026-08-08

## Context

Lodge discovers and displays Web URLs from operator annotations, redacted
reverse-proxy routes, and a small allowlist of conventional Web ports. A URL in
inventory proves neither that an HTTP server answers nor that the Hub can reach
it. Treating discovery as reachability would give the operator false confidence.

An active check is also a management-plane network capability. An unrestricted
request proxy could read internal response data, follow redirects to unexpected
targets, leak ambient proxy credentials, or be abused by a cross-site request.

## Decision

The Hub performs checks only after an authenticated, CSRF-protected operator
request. It checks the current de-duplicated service view with these bounds:

- at most 64 URLs per run and 8 concurrent requests;
- one `HEAD` request per URL, with a 5-second client deadline and a 15-second
  request budget;
- only absolute `http` or `https` URLs without user information;
- no redirect following, environment proxy, request credential, response-body
  read, or keep-alive reuse;
- only status class, latency, check time, and a small sanitized error category
  are retained.

HTTP 100–499 proves that an HTTP endpoint answered from the Hub's network view.
HTTP 500–599 is `degraded`; transport, DNS, TLS, and timeout failures are
`unreachable`. This does not claim Internet reachability or application-level
correctness.

The latest complete result set is replaced atomically in SQLite schema v5.
Stale URLs disappear from this projection when they are no longer in the
current service view; immutable fleet observations remain independent.

## Consequences

- The UI can distinguish never checked, reachable, degraded, and unreachable
  links without inventing evidence.
- Login, CSRF, concurrency, URL, time, and data-retention bounds are part of the
  release gate.
- Private and loopback destinations are intentionally allowed because Lodge is
  a private operations console. Therefore this feature remains an explicit
  privileged action and must never become an unauthenticated scheduled fetcher
  or a general response proxy.
- A successful result means only “the Hub received an HTTP response”; users may
  still encounter a different result from their browser's network location.
