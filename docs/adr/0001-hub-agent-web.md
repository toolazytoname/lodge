# ADR 0001: Hub + Agent with a Web client

- Status: accepted
- Date: 2026-08-07

## Decision

Keep a single Web Hub as the user interface and run a small Agent on each managed Linux host. The Hub pulls observations; Agents do not initiate inbound state pushes. The built frontend is embedded into the Go Hub binary.

## Consequences

- Users need only a browser and Tailscale, not a native Lodge client.
- One host failure does not prevent observation of other hosts.
- The Hub is security-sensitive because it holds Agent credentials and coordinates actions.
- Agent and Hub contracts require explicit versioning and compatibility handling.
