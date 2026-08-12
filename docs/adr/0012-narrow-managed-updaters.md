# ADR 0012: Narrow managed updaters instead of broad administrator sudo

- Status: accepted
- Date: 2026-08-12

## Context

The key-only `lodge-admin` account intentionally lost broad root access during
SSH hardening. A CLIProxyAPI upgrade then required repeated provider-console
handoffs merely to replace one known binary, restart one known service, and
check one loopback endpoint. Restoring `NOPASSWD: ALL`, an arbitrary shell, or a
generic file-copy command would erase the security boundary that hardening
created.

## Decision

Routine host-specific maintenance may use a root-owned, no-input updater only
when the executable, upstream, artifact selection, destination, service, health
check, backup, and rollback semantics are all fixed in reviewed code.

The sudo rule must use an exact command and explicitly deny arguments. The
helper must reject arguments again, serialize execution, validate root-owned
target metadata, authenticate release bytes with independent published
digests, replace atomically, wait for bounded health evidence, and restore the
previous artifact on failure. Output is typed and bounded; credentials,
configuration, response bodies, and raw journals do not cross the boundary.

The CLIProxyAPI adapter intentionally tracks stable upstream `latest`. This
authorization and its upstream-compromise risk are documented rather than
silently presented as equivalent to a root-pinned immutable release.

## Consequences

- Codex and the operator regain a one-command Tailnet workflow for the approved
  maintenance transaction without gaining a general root shell.
- New services receive no authority by analogy; each adapter and sudo rule is
  separately reviewed and installed.
- Initial installation still requires an existing recovery/root path because
  a secure system cannot grant itself new authority.
- The first slice is available over Tailnet SSH. Web exposure remains future
  work and must reuse the typed Agent/Hub operation audit rather than invoke a
  browser shell.
