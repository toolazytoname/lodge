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
