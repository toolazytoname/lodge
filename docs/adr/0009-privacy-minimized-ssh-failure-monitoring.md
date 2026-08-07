# ADR 0009: Privacy-minimized SSH failure monitoring

- Status: accepted
- Date: 2026-08-08

## Context

The operator previously noticed an SSH brute-force attack only after a server
became slow and could not answer which sources were involved. OpenSSH
authentication logs contain the needed evidence, but granting the Agent general
log access or copying raw records to the Hub would expose usernames, successful
logins, ports, and unrelated metadata. The managed fleet uses both local auth
files and journald, with materially different query performance.

## Decision

The non-root Agent calls one exact root-owned self-invocation,
`lodge-agent --collect-ssh-auth`. It accepts no caller input. The helper first
opens a regular, non-symlink, non-group/world-writable `/var/log/auth.log` or
`/var/log/secure`, reads at most the final 8 MiB, and rejects the result unless parsed timestamps prove that
the tail covers the complete previous ten minutes. If neither file exists, it
executes a fixed, five-second `journalctl` query where `_COMM=sshd` or
`SYSLOG_IDENTIFIER=sshd`. The helper recognizes failed password, public-key,
keyboard-interactive, and maximum-attempt messages.

Raw records are parsed and discarded inside that helper. Its only output is a
UTC window, total failures, and the top 20 canonical source IP/count pairs,
sorted by count. It never emits usernames, accepted logins, source/destination
ports, arbitrary log fields, or raw messages. Output, byte, time, entry, source,
and count limits fail closed.

The Hub stores the aggregate on the immutable Observation. A host-scoped
`ssh.bruteforce` event opens when a ten-minute window reaches 30 total failures
or 10 from one source. It remains active while the window has at least 10 total
or three from one source, and is critical at 100 total or 50 from one source.
Missing or invalid SSH telemetry carries an existing event rather than proving
recovery. Event detail shows at most the leading three source IPs.

This is an additive Agent v1 field: older Agents omit it, and older Hubs ignore
it, preserving rolling-upgrade compatibility.

## Consequences

- The operator learns which IPs generated the observed failures within the next
  normal Hub scrape, without centralizing raw authentication logs.
- An IP address is network evidence, not proof of a person's identity. Lodge
  makes no attribution or geolocation claim.
- The collector supports the two conventional local authentication-log paths
  plus systemd-journald OpenSSH. Dropbear, containerized SSH daemons, custom
  paths, and unrecognized message formats are explicit coverage gaps.
- Source IPs are sensitive inventory. They remain in authenticated event
  history and can reach an explicitly configured Webhook receiver.
