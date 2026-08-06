# ADR 0003: No arbitrary shell execution

- Status: accepted
- Date: 2026-08-07

## Decision

Lodge exposes typed, policy-approved operations instead of terminal access or arbitrary commands. Privileged execution uses fixed argv and root-owned target policy. The Agent remains outside privileged Unix groups.

## Consequences

- New operations require an explicit contract, policy, tests, and UI representation.
- Generic troubleshooting remains an SSH responsibility.
- Dynamic workload targets require a narrow privileged helper rather than sudo wildcards.
