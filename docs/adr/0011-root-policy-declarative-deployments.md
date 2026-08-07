# ADR 0011: Root-policy declarative deployments

- Status: accepted
- Date: 2026-08-08

## Context

Lodge needs a convenient Web deployment workflow without turning the Hub into
a Compose-file editor, image-tag resolver, secret store, or remote shell. A
compromised Hub, browser, non-root Agent, or workload must not be able to choose
an executable, host path, Compose project, service, environment, health target,
or mutable image tag.

Deployment failures also differ from ordinary action failures: changing a
container and then losing health requires an automatic attempt to restore the
known previous image. A database audit alone is not a rollback mechanism.
Stateful services need workload-specific backup and restore semantics, so a
generic first version must not claim to protect them.

## Decision

Each host may have `/etc/lodge-agent/deployments.json`, a regular, non-symlink,
root-owned mode `0600` policy. Missing policy means zero deployments. Version 1
accepts only explicitly `stateless` single-service Docker Compose stacks. Every
project directory, Compose file, and optional `.env` path must be clean,
absolute, root-owned, non-symlinked, and not group/world writable. The Compose
file must remain beneath its project directory.

The policy pre-registers the stack and service, a Docker-health or exact
`http://127.0.0.1:port/path` check, a 10–300 second health budget, and one or
more releases. Release images must be complete repository digest references of
the form `repository@sha256:<64 lowercase hex>`. Tags, registry credentials,
commands, environment values, backup commands, arbitrary URLs, and caller
arguments are not policy fields.

Sudoers adds two exact self-invocations:

- `lodge-agent --list-deployments`, a read-only validated projection; and
- `lodge-agent --execute-deployment`, a write helper that reads one bounded JSON
  deployment ID from standard input.

The Agent HTTP API accepts the ID in the path and requires an empty body. It
returns only typed release identity, summary, rollback status, and bounded error
category. Compose paths, environment, commands, Docker output, health response,
and raw errors never cross the root boundary. Ordinary actions and deployments
share both a non-root mutex and the root-owned non-blocking lock.

The root helper uses fixed `docker compose`, `docker image`, and `docker inspect`
argv without a shell. It validates the base and generated override with Compose,
pulls and inspects the exact digest, changes only the registered service with
`--no-deps`, and then verifies running plus the registered health policy.

Before the first change, the helper discovers the running container's immutable
repository digest; absence of a digest fails closed. Current and previous
release metadata plus the exact generated Compose override are one canonical,
root-only file under `/var/lib/lodge-agent/deployments`. The metadata is encoded
in the first comment and the whole file is regenerated and compared on read, so
a state/override mismatch is rejected. Candidate files are mode `0600`, synced,
atomically renamed, and followed by a directory sync.

If candidate apply, health, or state commit fails, the helper applies a generated
override for the pre-operation immutable image and verifies health again. A
successful recovery reports the original categorized failure with
`rollbackPerformed=true`; a failed recovery becomes `rollback_failed`. Explicit
rollback swaps the current and previous verified releases. There is no retry or
automatic replay above this host-local recovery sequence. Recovery has an
independent bounded deadline, so an exhausted candidate deadline cannot consume
the time reserved for restoring the previous service.

## Consequences

- Operators choose only a root-reviewed release ID; onboarding a stack or
  release remains an explicit host policy change.
- A mutable tag, unsafe Compose path, caller-provided YAML, public health URL,
  missing immutable current digest, invalid state file, or concurrent operation
  fails closed.
- Version 1 rejects databases, queues, and all other stateful services. They
  require dedicated, tested backup/restore adapters before policy support.
- Automatic rollback is best effort and its failure is visible. It cannot
  guarantee recovery from host loss, broken storage, an incompatible external
  dependency, or a release that corrupts data despite being declared stateless.
- The Hub and Web layer must re-list live authority, require exact confirmation,
  execute asynchronously with no retry, and durably audit success, recovered
  failure, and failed rollback before M7 is complete.
