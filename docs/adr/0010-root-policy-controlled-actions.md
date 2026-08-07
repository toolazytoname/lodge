# ADR 0010: Root-policy controlled actions

- Status: accepted
- Date: 2026-08-08

## Context

Lodge needs useful start, stop, restart, and recent-log operations without
turning the Web console into a remote shell. The previous Agent prototype
contained direct sudo entries for Docker prune, journal vacuum, and Caddy
restart. Those fixed commands were narrower than a shell, but they were not
bound to an explicit per-host target policy and encouraged adding one sudo rule
per UI button.

The non-root `lodge` process is part of the network-facing boundary. It must not
be able to choose an executable, append an argument, replace the policy, or run
two disruptive operations concurrently.

## Decision

The generated sudoers policy has exactly two action-related self-invocations:

- `lodge-agent --list-actions`, a read-only projection; and
- `lodge-agent --execute-action`, the only write entry.

Both command lines are exact and accept no positional arguments. The write
helper reads one bounded JSON object from standard input containing only a
stable action ID. It resolves that ID against `/etc/lodge-agent/actions.json`,
which must be a regular, non-symlink, root-owned mode `0600` file. Its parent
directory is root-owned mode `0750`, so the `lodge` account cannot replace the
file with `rename(2)`. A missing policy means an empty action list.

Policy targets are explicit systemd service units or Docker container names.
Names use narrow character grammars and the key must exactly equal
`kind:resource`. Each target independently approves a subset of `start`,
`stop`, `restart`, and `logs`. The policy cannot contain commands, executable
paths, shell text, environment, working directories, or additional arguments.

The root helper maps each approved tuple to an internally fixed argv and never
uses a shell. It applies byte, line, and time limits, checks state before and
after changes, and serializes actions with a root-owned non-blocking lock.
Recent logs are limited to 200 lines/64 KiB, normalized to UTF-8, stripped of
control characters, and redacted for common bearer, credential, key/value, and
URL-userinfo patterns. They are sensitive transient output and are never part
of the durable operation audit.

The non-root HTTP boundary lists only typed action definitions. Execution takes
the action ID from the URL and requires an empty body; it performs a second
definition lookup, permits one in-flight action, validates the typed root
result, and replaces helper/sudo errors with stable categories. Raw stderr,
argv, and process errors do not cross the Agent API.

## Consequences

- Adding a target requires an explicit root-owned host policy change; adding a
  new action kind requires code, contract, threat-model, and test changes.
- The Hub can never broaden host authority. Removing or invalidating the host
  policy immediately fails closed even if a stale browser still shows a button.
- Log redaction is defense in depth, not proof that arbitrary workload text is
  secret-free. Operators should approve log access only for suitable targets;
  the UI must label it sensitive and must not persist or copy it into audit
  records.
- Generic diagnostics, package upgrades, Docker prune, journal vacuum, and
  arbitrary command execution remain SSH responsibilities until separately
  designed typed operations exist.
