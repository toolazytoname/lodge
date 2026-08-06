# Tailnet-only management deployment

Lodge management endpoints use Tailscale Serve, never Funnel. Serve accepts
traffic from the tailnet and applies its access policy; Funnel accepts traffic
from the public internet. Login authentication remains enabled as a second
boundary.

This procedure follows the current [Tailscale Serve CLI reference](https://tailscale.com/docs/reference/tailscale-cli/serve),
[Funnel reference](https://tailscale.com/docs/reference/tailscale-cli/funnel),
and [grants syntax](https://tailscale.com/docs/reference/syntax/grants).

## Ports and flows

| Source | Destination | Port | Purpose |
| --- | --- | ---: | --- |
| Lodge operators | `tag:lodge-hub` | TCP 10000 | HTTPS Web console |
| `tag:lodge-hub` | `tag:lodge-agent` | TCP 8443 | Agent collection over encrypted Tailnet |
| Lodge operators | Hub and Agent tags | TCP 22 | SSH recovery path |

The Go processes still bind only to loopback: Hub `127.0.0.1:9102` and Agent
`127.0.0.1:9101`. Tailscale terminates HTTPS for the browser-facing Hub. The
Hub-to-Agent hop uses a raw TCP Serve forward inside Tailscale's authenticated,
encrypted tunnel; Lodge still speaks HTTP to the loopback Agent, but it is not
internet plaintext. TCP forwarding also works with a Tailscale IP when the Hub
does not use MagicDNS and does not depend on an HTTP `Host` routing rule.

## Access policy

Merge `deploy/tailscale-grants.example.hujson` into the policy in the Tailscale
admin console and replace the operator identity. Assign `tag:lodge-hub` to the
Hub node and `tag:lodge-agent` to every managed server.

Grants are additive. A narrow Lodge grant does not override an existing
allow-all rule. Keep a cloud console or existing SSH session open, verify the
new rules, and only then remove broad rules that make the tailnet effectively
flat.

Required positive checks:

- an operator device can reach Hub HTTPS 10000 and SSH 22;
- the Hub can reach every Agent on Tailnet TCP 8443 and receive Agent HTTP 401;
- an operator can reach server SSH 22.

Required negative checks:

- a non-operator tailnet device cannot reach Hub 10000;
- an ordinary tailnet device cannot reach Agent 8443;
- an Agent cannot reach another Agent on 8443.

## Endpoint preflight and migration

The helper is audit-only unless called with `apply`. It requires Tailscale
1.52+, Python 3, `ss`, and `curl`. Before changing anything it verifies the
local listener is loopback-only and responding.

```bash
# Expected to fail while the Hub still uses public Funnel.
sudo deploy/tailnet-management.sh check hub

# Save current status, disable Funnel on 10000, enable persistent HTTPS Serve,
# and verify the resulting private route.
sudo deploy/tailnet-management.sh apply hub

# Run on each Agent after Lodge is listening locally. Agent Serve uses raw TCP
# forwarding inside the encrypted tailnet so IP-based Hub URLs remain reliable.
sudo deploy/tailnet-management.sh apply agent
```

Defaults can be overridden with positional ports:

```bash
sudo deploy/tailnet-management.sh apply hub 9102 10000
sudo deploy/tailnet-management.sh apply agent 9101 8443
```

Every apply writes owner-only pre-change evidence under
`/var/lib/lodge/tailscale-backups/<UTC>-<pid>/`. It intentionally never restores
a public Funnel automatically. When an older Lodge Agent route uses HTTP Serve,
the helper verifies that it points to the expected loopback Agent before
disabling that exact route and replacing it with TCP forwarding.

## Verification

On the server:

```bash
sudo deploy/tailnet-management.sh check hub
tailscale funnel status --json
tailscale serve status --json
```

The check passes only when the selected port has no `AllowFunnel: true`, HTTPS
Serve points to the expected loopback listener, and the local process responds.

From an authorized tailnet device, open the Hub URL and complete login, refresh,
and logout. From a device outside the tailnet (for example a phone with
Tailscale disabled), confirm the URL cannot reach Lodge. Do not infer public
privacy only from a successful tailnet request.

## Recovery

Keep a second SSH session open during the migration. If browser access fails,
do not re-enable Funnel. Use an SSH port forward while correcting grants or
Serve:

```bash
ssh -L 9102:127.0.0.1:9102 bytedragon
```

Then open `http://127.0.0.1:9102` locally. The production cookie is `Secure`, so
the full login flow still requires HTTPS; use the tunnel for diagnostics and
rerun `tailnet-management.sh apply hub` to restore private HTTPS access.

The pre-change status files are audit and troubleshooting evidence. They are
not fed back automatically because restoring a captured Funnel configuration
would recreate the public exposure this migration removes.
