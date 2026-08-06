# ADR 0002: Tailnet-only management plane

- Status: accepted
- Date: 2026-08-07

## Decision

The Hub management UI and every Agent endpoint are private management services. They use Tailscale Serve or direct tailnet connectivity, with grants limiting sources and ports. Tailscale Funnel is reserved for explicitly public, non-management services.

## Consequences

- A device must join the authorized tailnet before opening Lodge.
- Internet password scanning no longer reaches the management UI.
- Public marketing or status pages, if required later, must be separate deployments without operation capability or sensitive inventory.
