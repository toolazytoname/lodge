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
   through the authenticated service API to return assets without sudo errors;
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
- deployment/checksum/backup or recovery evidence is recorded without secrets.
