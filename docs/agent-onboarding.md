# Agent onboarding

Agent enrollment is a credential-handling operation. The Agent bearer token
must never appear in a command-line argument, terminal output, shell history,
Git, SQLite, or an operational log. It belongs only in the Agent token file,
the Hub owner-private config, and process memory.

## One host at a time

Keep a second SSH or cloud-console recovery path while changing a host. For
each server:

1. record the OS, architecture, Tailscale state, disk headroom, current Lodge
   service state, and any existing listeners;
2. build the Agent with the supported pinned Go toolchain and verify its
   checksum before installation;
3. run `deploy/install-agent.sh`, then confirm the `lodge` account has no
   privileged groups, the service is active, `--check` succeeds, and the token
   is owner-only; the installer validates both the generated sudoers candidate
   and the complete host policy, restores the prior Lodge policy on any new
   error (without silently enabling unrelated invalid host policy),
   checks the real service process has `NoNewPrivs=0`, and requires discovery
   through the authenticated service API to return assets, a valid SSH failure
   summary, typed controlled-action and declarative-deployment lists without
   sudo errors;
4. run `deploy/tailnet-management.sh apply agent`, verify Tailscale Funnel is
   disabled, and test port 8443 from the Hub;
5. transfer the token as an owner-only file, atomically add it to the Hub
   config, restart the Hub, and wait for a successful observation;
6. delete every exact staging file and record the evidence. Only then move to
   the next host.

Do not use `cat` to display a token and do not interpolate a token into `curl`,
`ssh`, or a Lodge command. SSH login banners can also corrupt a piped token, so
do not stream a token through an unverified `ssh host cat ... | ssh hub ...`
pipeline.

The generated read-only sudoers policy includes one exact self-invocation,
`/usr/local/bin/lodge-agent --collect-process-origins`. The binary is
root-owned and the `lodge` account cannot replace it. This helper reads only
PID/UID, process and executable basenames, and the working-directory basename
plus a one-way fingerprint. It never reads or emits `cmdline`, environment
variables, or the full working-directory path. Additional arguments do not
match either the Agent allowlist or sudoers policy.

A second exact self-invocation, `--collect-compose-metadata`, runs one fixed
Docker query inside the root-owned Agent and emits only validated
`com.docker.compose.project` and `com.docker.compose.service` tuples. It does
not return the label map, working directory, config paths, environment, or
command line. A fixed systemd read returns unit identity, state, and fragment
path; the Agent uses the path only in memory to classify operator-managed units
and emits no path. All sudoers argv arrays have no caller-controlled target or
wildcard.

A third exact self-invocation, `--collect-proxy-routes`, handles Caddy and
Nginx discovery. It reads only standard host configuration and explicit Docker
bind mounts selected internally from validated container metadata. The helper
does not accept a container, file, or command argument from `lodge`; it does not
read container environment variables or execute a command inside a container.
For host Nginx, it expands at most 128 files and 16 include levels strictly
beneath `/etc/nginx`, with a 4 MiB aggregate limit. In-tree relative or absolute
symlinks are accepted; targets outside that root, variable includes, device
files, and over-limit trees are rejected. The exact conventional Certbot TLS
policy include is ignored without being read because it carries no route
directives. This avoids `nginx -T` touching TLS material hidden by the Agent
sandbox. Raw configuration, includes, certificate/key paths, headers, and
authentication directives remain in root memory. Output is limited to validated
HTTP(S) scheme/host/port/path records, a bounded route kind, and credential-free
upstream `host:port` authorities. Named Nginx virtual hosts that directly serve
`root`, `alias`, `try_files`, or `index` content also emit a static `/` route
without revealing the filesystem path. Unsupported Docker imports/includes produce a bounded warning
rather than raw content.

A fourth exact self-invocation, `--collect-ssh-auth`, reads an 8 MiB bounded tail
from the fixed `/var/log/auth.log` or `/var/log/secure` path and rejects a
symlink or group/world-writable file. It fails if that tail does not cover the
previous ten minutes. If neither regular file exists,
it runs one fixed five-second `journalctl` query. Parsing and aggregation happen
inside the root-owned helper. It emits only total failures and at most 20
canonical source IP/count pairs; usernames, accepted logins, ports, log
metadata, and raw messages never cross the privilege boundary. The helper
recognizes failed password/public-key/keyboard-interactive and maximum-attempt
records. It does not accept a unit, time window, filter, or file path argument
from `lodge`.

A fifth exact self-invocation, `--collect-security-posture`, evaluates only
fixed local commands for the effective OpenSSH settings, SSH listener shape,
UFW, Fail2Ban, and Tailscale runtime state. It accepts no argument. Before the
result leaves root it is reduced to the closed values `enabled`, `disabled`,
`restricted`, `unavailable`, or `unknown`; it never emits users, authorized
keys, firewall rules, network addresses, command paths, raw output, cloud
security-group state, or a claim that wildcard SSH is Internet reachable.
`unknown` and `unavailable` are deliberately not safe states. This is a current
runtime posture, not a durable historical configuration record.

Controlled operations use two additional exact self-invocations. The read-only
`--list-actions` helper projects `/etc/lodge-agent/actions.json`; the write
entry `--execute-action` reads one bounded JSON action ID from standard input.
Neither accepts a target, command, path, or action as an argv value. The root
helper resolves the ID against the policy and maps only approved systemd units
or Docker containers to internally fixed start/stop/restart/log argv arrays.

Declarative deployments add an exact read-only `--list-deployments` projection
and exact write `--execute-deployment` helper. The latter reads only one bounded
deployment ID from standard input. `/etc/lodge-agent/deployments.json` may
register only explicitly stateless Compose services, immutable sha256 image
references, and Docker or loopback HTTP health checks. The Hub cannot supply a
Compose file, path, service, image, environment, command, health URL, or backup
procedure. Root validates the entire Compose path chain, captures the running
immutable image as the first rollback point, changes one service with fixed argv,
and automatically reapplies and verifies the old image when deployment fails.
State and the generated override live in root-only
`/var/lib/lodge-agent/deployments`; the service has a systemd writable mount for
that exact directory but Unix mode `0700` prevents the `lodge` account from
entering it. See [ADR 0011](adr/0011-root-policy-declarative-deployments.md).

The installer makes `/etc/lodge-agent` root-owned mode `0750` with group
`lodge`, preserves or creates the token as `lodge:lodge` mode `0600`, and leaves
the directory read-only to the service. If `actions.json` exists, installation
requires it to be a regular, non-symlink, `root:root` mode `0600` file and
validates it before restarting. `deployments.json` has the same owner, mode,
type, parent-directory, and fail-closed rules; its configured Compose paths and
existing rollback state must also validate. If either policy is absent, its
capability list is empty. To
enable a reviewed policy, start from `deploy/agent-actions.example.json`, then
install it with exact ownership and mode before rerunning the Agent installer.
For deployment policy, start from `deploy/agent-deployments.example.json` and
follow [`docs/declarative-deployments.md`](declarative-deployments.md).
Never make the directory writable by `lodge`: file ownership alone does not
prevent a writable-directory rename replacement.

Owner-service maintenance is a `lodge-admin` class, not an Agent HTTP API. If
`lodge-admin` exists, the installer writes `/etc/sudoers.d/lodge-admin-operator`
from `--print-admin-sudoers`. `/etc/lodge-agent/operator.json` lists opted-in
non-root service-owner accounts such as `ecs-user`. A missing file disables the
class. `OPERATOR_USERS=ecs-user ./install-agent.sh` can create that file on first
install; later services under the same owner need no new sudoers rule. See
[`docs/operator-maintenance.md`](operator-maintenance.md) and
[ADR 0013](adr/0013-owner-service-operator.md).

## Atomic Hub enrollment

Copy `/etc/lodge-agent/token` through a trusted channel into a uniquely named,
owner-only staging file on the Hub. The staging file must be readable by the
owner of `/etc/lodge-hub/config.json`. Then run the Hub binary as that owner:

```bash
sudo -u lodge /usr/local/bin/lodge-hub \
  --config /etc/lodge-hub/config.json \
  --upsert-agent tencent \
  --agent-name Tencent \
  --agent-url http://100.71.151.6:8443 \
  --agent-public-host 43.143.252.243 \
  < /etc/lodge-hub/.tencent-token-import
```

The example address is a placeholder; obtain the real Tailscale IPv4 address
from Tailscale status. A MagicDNS name is also valid when the Hub resolves it.
The TCP Serve forward deliberately supports either addressing form.
`--agent-url` accepts only an HTTP(S) base URL with no
credentials, query, fragment, or application path. `--agent-public-host` is
optional and accepts a DNS name or IP without a port.

The command reads at most 4096 token bytes from non-interactive standard input,
validates the full config, writes an owner-only temporary file, syncs it, checks
that the source was not concurrently replaced, atomically renames it, syncs the
directory, and preserves the original owner/group. It never prints the token.
Repeating an identical enrollment does not rewrite the file.

After a successful import, restart `lodge-hub`, require the new host to become
online in the authenticated API, and remove the exact staging file. A failed
validation leaves the original config unchanged.

## Acceptance evidence

Enrollment is complete only when all of these are true:

- Agent process binds to loopback and the tailnet route has no Funnel;
- `lodge` is a non-login account outside `docker`, `sudo`, `wheel`, and `adm`;
- Agent token and Hub config are mode `0600` and absent from SQLite/log output;
- Hub-to-Agent ping succeeds through tailnet port 8443;
- Hub reports a current successful observation, not merely a configured host;
- the authenticated Agent status contains a current `ssh` summary and no SSH
  collector warning;
- the authenticated Agent actions response is typed; an absent policy returns
  an empty list, and direct legacy write commands plus extra helper argv are
  rejected by sudo;
- the authenticated deployment response is typed and contains no host paths or
  Compose data; absent policy is empty, dynamic helper argv is rejected, and any
  enabled stack has a tested immutable rollback point;
- deployment/checksum/backup or recovery evidence is recorded without secrets.
