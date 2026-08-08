---
type: source
status: active
sources:
  - "[[2026-08-06-lodge-readonly-audit]]"
  - "[[2026-08-08-m5-history-alerts]]"
  - "[[2026-08-08-m7-declarative-deployments]]"
---

# M8 SSH hardening baseline

## Scope

After M7's fail-closed rollout, a fresh read-only five-host probe examined the effective OpenSSH settings, listener shape, local firewall/Fail2Ban availability and Tailscale runtime state. It intentionally did not edit `sshd_config`, UFW, cloud security groups, Fail2Ban, users, or Tailscale ACLs.

## Finding

All five hosts have Tailscale running and public-key authentication enabled, but each has a wildcard port-22 listener, effective `PasswordAuthentication yes`, and effective `PermitRootLogin yes`. The current `MaxAuthTries`/`LoginGraceTime` values are 6/120. bytebunny, bytedragon and Ali also have inactive UFW and no active Fail2Ban; tencent has both controls active; banwagong has active UFW but no active Fail2Ban.

This confirms a material shared-password/root-SSH exposure. It does **not** prove Internet reachability: host-local listener and UFW data cannot see cloud security groups, carrier filtering or provider consoles.

## Decision boundary

The correct next production transaction is a per-host SSH hardening rollout, not an automatic bulk change. Before disabling the existing route it needs a known cloud-console/recovery path and a separately proven `lodge-admin` tailnet key session. The proposed initial profile disables password, keyboard-interactive and root login while retaining public-key auth, and lowers the first-attempt limits to 3/30 seconds. Only then may public 22 be removed or allowlisted at the provider edge.

The full gates, rollout order, rollback evidence and acceptance record are in [SSH hardening rollout](../../docs/ssh-hardening.md). M8 remains in progress. This does not alter M7's separate decision: current business services still provide zero stateless v1 deployment targets.

## Current posture implementation

Agent `0.8.0` adds a fifth exact root self-invocation, `--collect-security-posture`. It has no caller input and reduces fixed local OpenSSH/listener/UFW/Fail2Ban/Tailscale checks to seven closed enum fields before data crosses root. Users, keys, firewall rules, addresses, raw command output and cloud edge evidence are excluded. Hub exposes this only in its live runtime host summary; it intentionally does not write a potentially stale SSH setting into historical observations. Security UI covers normal, unavailable, unknown and offline states across desktop and mobile visual acceptance.

## Ali pilot outcome

After the operator confirmed console recovery and approved the named non-root
key administrator, Ali became the single completed M8 pilot. Its `lodge-admin`
Tailnet login passed Tailscale SSH check mode; a root-owned drop-in then passed
`sshd -t` and reload. Effective password, keyboard-interactive, and root login
are disabled; public-key authentication remains enabled and 3/30 are the new
attempt/grace limits. A fresh Tailnet administrator session succeeded while
public root-key and password-only attempts were rejected. The Agent reports the
same minimized current posture. No claim is made about its cloud edge, and no
setting on the remaining four hosts changed.

## bytebunny pilot outcome and Tailnet boundary

bytebunny subsequently completed the same OpenSSH-side pilot. Its initial
late-numbered drop-in was safely caught as ineffective for password auth because
cloud-init's earlier `50-cloud-init.conf` supplied the first value. The backup
was retained, the Lodge drop-in was moved before cloud-init, and effective
OpenSSH verification then reported password, keyboard-interactive, and root
login disabled with public-key authentication and 3/30 limits. Fresh
`lodge-admin` Tailnet login and public root/password rejection tests passed.

The separate Tailnet SSH policy boundary was resolved on 2026-08-08. Its sole
rule now retains member-to-self check mode but permits only the exact local
`lodge-admin` user. Fresh Ali and bytebunny tests reject Tailnet `root` and
retain successful `lodge-admin` sessions, completing remote-root closure for
both access planes on the pilots.

## bytedragon rollout outcome

bytedragon then passed the same recovery-gated rollout. The operator key,
locked-password `lodge-admin`, exact sudo allowlist, backup, and fresh Tailnet
session were verified before change. Its cloud-init ordering was handled with an
`01-` root-owned drop-in; `sshd -t`, reload, and effective OpenSSH output confirm
password, keyboard-interactive, and root login are disabled with 3/30 limits.
Fresh `lodge-admin` succeeds and both public root-key and password-only attempts
are rejected. Cloud-edge and local firewall work remains outside this rollout.

## tencent rollout outcome

tencent passed the same recovery-gated access rollout while preserving its
already-active UFW and Fail2Ban. A duplicate pre-existing sudo command alias
was safely caught before sshd change; the new account's whitelist was rewritten
as exact commands without an alias and passed full `visudo -c`. A root-owned
`01-` drop-in then passed test and reload. The effective 3/30 key-only non-root
OpenSSH profile, fresh `lodge-admin` session, and rejection of public root-key,
password-only, and Tailnet root were all verified.
