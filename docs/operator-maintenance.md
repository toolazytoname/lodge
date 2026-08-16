# Owner-service operator

`lodge-admin` may maintain opted-in non-root service-owner accounts. This is
not a shell, not Tailnet SSH as the owner, and not an Agent HTTP API.

Authorization is the existing Tailnet `lodge-admin` identity plus a root-owned
owner list. The network-facing `lodge` account cannot invoke these helpers.

## Policy

`/etc/lodge-agent/operator.json` is a regular, non-symlink, `root:root` mode
`0600` file. A missing or empty file disables the class.

```json
{
  "version": 1,
  "owners": ["ecs-user"]
}
```

Denied names include `root`, `lodge`, `lodge-admin`, and other system accounts.
The owner must exist, have UID >= 1000, and use a normal login home. Adding
another service under an already-listed owner needs no new sudoers rule.

First install can create the file once:

```bash
OPERATOR_USERS=ecs-user sudo ./install-agent.sh /path/to/lodge-agent
```

Later installs do not overwrite an existing policy. Start from
`deploy/agent-operator.example.json` when writing the file by hand.

## Exact sudo

If `lodge-admin` exists, the installer writes `/etc/sudoers.d/lodge-admin-operator`
from `lodge-agent --print-admin-sudoers`:

```text
lodge-admin ALL=(root) NOPASSWD: \
    /usr/local/bin/lodge-agent --list-operator, \
    /usr/local/bin/lodge-agent --execute-operator
```

Both helpers accept no extra argv. stdin JSON is bounded and rejects unknown
fields. Supported operations:

- `read-file` / `write-file` / `list-dir` under that owner's home, after
  rejecting absolute paths, `..`, symlink escape, and credential locations
  (`.ssh`, `.gnupg`, `.aws`, and similar);
- `write-file` only for an existing regular file owned by that user, with a
  256 KiB bound, optional SHA-256 compare-and-swap, and a root-only backup
  under `/var/lib/lodge-agent/operator-backups`;
- `unit-status` / `unit-restart` only when `systemctl show -p User` equals the
  allowed user and the unit is not a denied system service.

## Daily use

```bash
sudo -n /usr/local/bin/lodge-agent --list-operator

printf '%s' '{"owner":"ecs-user","op":"list-dir","path":".config/mihomo"}' \
  | sudo -n /usr/local/bin/lodge-agent --execute-operator

printf '%s' '{"owner":"ecs-user","op":"read-file","path":".config/mihomo/config.yaml"}' \
  | sudo -n /usr/local/bin/lodge-agent --execute-operator

printf '%s' '{"owner":"ecs-user","op":"write-file","path":".config/mihomo/config.yaml","content":"...","sha256":"..."}' \
  | sudo -n /usr/local/bin/lodge-agent --execute-operator

printf '%s' '{"owner":"ecs-user","op":"unit-restart","unit":"mihomo.service"}' \
  | sudo -n /usr/local/bin/lodge-agent --execute-operator
```

This is not a remote editor for root files, packages, or Hub-projected browser
actions. Hub projection must reuse these helpers rather than invent a second
path. See [ADR 0013](adr/0013-owner-service-operator.md).
