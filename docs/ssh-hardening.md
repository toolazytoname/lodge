# SSH hardening rollout

This runbook turns Lodge's SSH attack visibility into a safe production hardening change. It deliberately separates *observing* an SSH failure spike from *changing* an SSH entry path: a bad SSH or firewall change can remove the only recovery route to a host.

## 2026-08-08 read-only baseline

The following is an effective `sshd -T` and local-service snapshot, collected without changing a host. It does **not** prove whether a cloud security group or upstream network allows port 22 from the Internet.

| Host | SSH bind | Password auth | Root remote login | Local firewall | Fail2Ban | Tailscale |
| --- | --- | --- | --- | --- | --- | --- |
| bytebunny | wildcard IPv4 + IPv6 | enabled | enabled | UFW inactive | unavailable/inactive | running |
| bytedragon | wildcard | enabled | enabled | UFW inactive | unavailable/inactive | running |
| Ali | wildcard IPv4 | enabled | enabled | UFW inactive | unavailable/inactive | running |
| tencent | wildcard IPv4 + IPv6 | enabled | enabled | UFW active | active | running |
| banwagong | wildcard IPv4 + IPv6 | enabled | enabled | UFW active | unavailable/inactive | running |

All five effective configurations also report public-key authentication enabled, `MaxAuthTries 6`, and `LoginGraceTime 120`. Every host therefore still accepts password attempts on a wildcard SSH listener. Tailscale is available on all five, but availability alone does not remove the public listener.

Agent 0.8.0 adds a current-only Security-page projection for the same bounded facts: wildcard/restricted SSH listener, password/root/public-key posture, UFW, Fail2Ban, and Tailscale. The root helper returns only a small fixed vocabulary; it does not retain keys, users, firewall rules, addresses, raw command output, or cloud-edge policy. `unknown`/`unavailable` are deliberately visible as uncertain, and the panel cannot prove whether port 22 is Internet reachable.

## Visibility rollout acceptance

After the final push and PR quality gates passed, all five hosts received Agent
`0.8.0` and the Hub received `0.9.0`. Every Agent service supplied the complete
seven-field closed vocabulary, and an attempted invocation with an added helper
argument was rejected on every host. The Hub's transaction completed with a
rollback bundle and post-deploy database backup; schema 7 integrity, 5 configured
hosts, the served Security UI, unauthenticated API rejection, loopback binding,
Tailnet Serve, recent-log configured-secret exclusion, and a fresh 5/5
Hub-local authenticated posture pull were verified. This is
an observability acceptance record only: no SSH daemon, account, password,
firewall, Fail2Ban, or cloud-edge setting was changed.

## Ali pilot acceptance — 2026-08-08

With an explicitly approved console/recovery path, the Ali pilot now has a
locked-password `lodge-admin` account with the operator's existing Mac public
key. Its home and `.ssh` directory are owner-only; its sudoers entry validates
with `visudo` and grants only exact `sshd -t`, `systemctl status/reload
ssh.service`, and bounded SSH journal commands. Direct arbitrary sudo was
rejected. The Tailnet endpoint's ED25519 fingerprint was independently matched
against the host key obtained through the pre-existing access path before being
trusted locally.

The fresh `lodge-admin` Tailnet key login subsequently passed Tailscale SSH
check-mode verification. A root-owned drop-in then set `PasswordAuthentication
no`, `KbdInteractiveAuthentication no`, `PermitRootLogin no`,
`PubkeyAuthentication yes`, `MaxAuthTries 3`, and `LoginGraceTime 30`.
`sshd -t` passed before `systemctl reload ssh.service`; the resulting effective
settings were read back. A new Tailnet `lodge-admin` login remained successful,
while both public root-key and password-only login attempts were rejected.
Lodge's current security posture also reports password/root disabled and
public-key enabled. The local `lodge-ali` SSH alias points to the MagicDNS host
as this non-root account, and its exact sudo commands work; extra arguments and
arbitrary sudo remain rejected.

This pilot does not yet claim that public port 22 is closed at the cloud edge,
or that the other three hosts are hardened. Its rollback material remains under
the root-only Ali pilot backup directory and console recovery remains the
break-glass path.

## bytebunny pilot outcome — 2026-08-08

bytebunny completed the same approved non-root account, key verification,
limited sudo, backup, fresh Tailnet login, `sshd -t`, reload, and rejection
checks. Its initial `99-` drop-in did not disable passwords because cloud-init's
`50-cloud-init.conf` is read first by OpenSSH and explicitly sets
`PasswordAuthentication yes`. The failed-order copy remains in the root-only
backup directory; the validated Lodge drop-in was placed before cloud-init as
`01-lodge-hardening.conf`, then re-tested and reloaded. Effective OpenSSH now
has password and keyboard-interactive authentication disabled, root login
disabled, public-key authentication enabled, and 3/30 limits.

Both completed pilots also revealed a separate access plane: the then-current
Tailscale SSH policy could map a Tailnet `root` request directly to local root.
The operator approved the preferred resolution on 2026-08-08. The visual policy
was changed from `autogroup:nonroot, root` to the exact `lodge-admin` local
user, retaining `autogroup:member` → `autogroup:self` and check mode. Fresh
Tailnet tests reject `root` on Ali and bytebunny while both `lodge-admin`
sessions remain successful. This closes the independent Tailnet root path;
future hosts must create and verify `lodge-admin` before relying on Tailscale
SSH for administration.

## bytedragon rollout outcome — 2026-08-08

bytedragon had the same `50-cloud-init.conf` ordering characteristic as
bytebunny, so its approved root-owned hardening drop-in was installed as
`01-lodge-hardening.conf` from the start. A locked-password `lodge-admin` with
the operator's existing Mac key and the exact maintenance sudo whitelist was
created first; a fresh Tailnet session and exact sudo test succeeded before
OpenSSH was changed. `sshd -t` passed, `ssh.service` reloaded, and effective
OpenSSH reports password, keyboard-interactive, and root login disabled with
public-key enabled and 3/30 limits. A fresh `lodge-admin` session still
succeeds, while public root-key and password-only attempts to its public address
are both rejected. Its root-only rollback copy remains under the dated backup
directory; cloud-edge port 22, UFW, and Fail2Ban are still separate gates.

## tencent rollout outcome — 2026-08-08

tencent already had active UFW and Fail2Ban, but retained password/root
OpenSSH exposure. Its dedicated `lodge-admin`, key, dated rollback copy, and
fresh Tailnet session were verified before changing sshd. An existing sudo
environment exposed a duplicate command-alias warning when the shared alias was
first introduced; no SSH setting was changed until the whitelist was rewritten
without an alias and `visudo -c` passed the complete policy set. Its new `01-`
drop-in then passed `sshd -t` and reload. Effective OpenSSH has password,
keyboard-interactive, and root disabled, public-key enabled, and 3/30 limits.
Fresh `lodge-admin` succeeds; public root-key, password-only, and Tailnet root
attempts are rejected. Existing UFW/Fail2Ban were deliberately left intact.

## banwagong rollout outcome — 2026-08-08

banwagong had active UFW but inactive Fail2Ban and the same password/root
OpenSSH exposure. Its backup, locked-password key administrator, fresh Tailnet
session, and exact sudo behavior were verified before the `01-` drop-in passed
`sshd -t` and reload. Effective OpenSSH is key-only/non-root with 3/30 limits;
fresh `lodge-admin` succeeds, while public root-key, password-only, and Tailnet
root attempts are rejected. A pre-existing `/etc/sudoers.d/hermes-ro` file has
unsafe permissions and causes full `visudo -c` to report an error. Lodge did not
modify that unrelated file: its own exact sudo entry parsed, is mode 0440, and
was exercised successfully. Correcting the existing file would enable whatever
policy it contains, so it is a separately reviewed security task rather than an
automatic chmod.

## bytedragon cloud-edge outcome — 2026-08-08

After visual verification of the instance ID and its single attached `Default`
security group, the operator removed only the inbound TCP 22 rule sourced from
`0.0.0.0/0`. Other business, inter-security-group, and ICMP rules were left
unchanged. A fresh probe to `115.191.48.113:22` timed out, while a fresh
`lodge-admin` Tailnet session succeeded. This is the first independently
verified cloud-edge closure; it must not be extrapolated to the remaining hosts.

## bytebunny cloud-edge outcome — 2026-08-08

The operator separately removed bytebunny's Internet-wide TCP 22 rule in its
own Fire Volcano Engine account. A fresh public probe to `115.191.29.26:22`
timed out, while a fresh `lodge-admin` Tailnet session succeeded. Together with
bytedragon this verifies two independent cloud accounts rather than assuming a
shared security-group result.

## Ali cloud-edge outcome — 2026-08-08

Ali did not retain a standalone TCP 22 rule. Instead, the associated security
group had an Internet-wide all-traffic rule labeled `gost 转发`, which covered
SSH and the currently listening public proxy ports 8388 and 10809. The operator
confirmed the exposure was no longer needed and removed that one broad rule.
Fresh public TCP probes to 22, 8388, and 10809 all timed out; a fresh
`lodge-admin` Tailnet session remained successful. This intentionally retires
that public gost path rather than silently replacing it with guessed ports.

The initial risk priority was bytebunny, bytedragon, and Ali: each then had
password authentication and no verified host-level firewall or Fail2Ban layer.
All five hosts have since completed the recovery-gated key-only/non-root SSH
closure; this historical baseline must not be read as their current SSH posture.
The desired end state is not merely "Fail2Ban installed". Daily administration
uses a named non-root key account over the Tailnet; public port 22 is removed or
narrowly allowlisted at the cloud edge; and any deliberate local firewall or
Fail2Ban exception is recorded and reviewed.

## Tencent cloud-edge outcome — 2026-08-08

The operator removed the Internet-wide TCP 22 rule from the Tencent Lighthouse
firewall for the Beijing instance. A fresh TCP probe to `43.143.252.243:22` timed
out, while a new `lodge-admin` session over Tailnet succeeded. Probes to ports 8388
and 10809 also timed out; no business firewall rule was edited.

## Non-negotiable safety gates

Do not make a host change until all gates for that one host are true:

1. A cloud-console or rescue-mode recovery path is known and tested enough to reach the host if SSH becomes unavailable.
2. A distinct `lodge-admin` non-root account exists with a verified operator public key and narrowly documented sudo access. Do not reuse a shared password as a break-glass mechanism.
3. A second, already-authenticated SSH session remains open while the new connection is tested. Keep the existing session until post-change checks finish.
4. A fresh public-key session to the host's Tailscale address or MagicDNS name succeeds as `lodge-admin`. This must be a separate connection, not an assumption based on the old root session.
5. The candidate SSH configuration passes `sshd -t` before reload. The systemd reload must succeed; never restart the daemon as the first step.
6. A rollback command and the previous SSH drop-in are already present in the still-open recovery session. Firewall and cloud-edge changes have their own documented rollback route.

These gates are deliberately per-host. A successful change to one host is not evidence that another distribution, provider, or existing service layout is safe to change.

## Ordered rollout

Use a low-impact host first, then wait for stable Lodge observations and normal operator access before moving to the next host.

1. Record the cloud provider's current port-22 rule and the approved personal recovery source. Do not infer cloud firewall state from `ss` or UFW.
2. Provision and verify the dedicated key-only administrator account over the tailnet, retaining the current session and console recovery route.
3. Add a root-owned SSH drop-in that disables password and keyboard-interactive authentication and disables root login. Retain public-key authentication. Use a conservative first profile: `PasswordAuthentication no`, `KbdInteractiveAuthentication no`, `PermitRootLogin no`, `PubkeyAuthentication yes`, `MaxAuthTries 3`, and `LoginGraceTime 30`.
4. Run `sshd -t`, reload rather than restart, then prove a fresh tailnet `lodge-admin` session. Inspect the effective settings with `sshd -T`.
5. Restrict cloud-edge port 22 to no public source (preferred) or to the documented temporary recovery source. Do not expose a tailnet management host through an Internet-wide SSH rule just because it has key auth.
6. Enable a host firewall only after enumerating every required existing port and interface, including Tailscale. Enable Fail2Ban where the distribution's OpenSSH logs are supported. Neither control substitutes for steps 2--5.
7. Confirm that Lodge still receives a current SSH failure summary and that the Security page can surface a new brute-force spike. Do not manufacture a weak SSH configuration merely to produce test traffic.

## Acceptance record

For each host, preserve the following evidence in the operations log without copying passwords, public keys, tokens, raw authentication logs, or source IPs:

- pre/post effective values for password authentication, root login, public-key authentication, `MaxAuthTries`, and `LoginGraceTime`;
- successful fresh non-root tailnet key-login timestamp;
- SSH configuration test and reload result;
- cloud-edge rule decision and rollback reference;
- firewall and Fail2Ban state, with any deliberate exception;
- Lodge agent health, SSH-summary freshness, and any active SSH event state.

Only after all five records are complete can the roadmap claim that password and root SSH are closed. The previous shared password must then be retired from the password manager and never reused as a server credential.
