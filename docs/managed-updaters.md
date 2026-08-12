# Narrow managed updaters

Lodge keeps `lodge-admin` deliberately unable to run an arbitrary root shell.
Repeatedly opening a provider console for a known maintenance transaction is
also undesirable: it makes a routine upgrade slow, hard to audit, and easy to
perform inconsistently.

The first narrow updater covers the existing CLIProxyAPI systemd service on
banwagong. It restores unattended maintenance without restoring broad sudo.

## Security boundary

`/usr/local/sbin/lodge-upgrade-cliproxyapi` is installed as `root:root 0755`.
The sudoers rule approves the exact path with the sudoers empty-argument marker
`""`. `lodge-admin` may therefore run:

```bash
sudo -n /usr/local/sbin/lodge-upgrade-cliproxyapi
```

It may not append a version, URL, path, service name, shell fragment, or any
other argument. The helper also rejects arguments itself.

The updater fixes all authority in root-owned code:

- GitHub repository: `router-for-me/CLIProxyAPI`;
- stable `releases/latest` API (drafts and prereleases are rejected);
- Linux amd64 release asset and exact archive member;
- `/root/cliproxyapi/cli-proxy-api` and `cliproxyapi.service`;
- loopback-only `http://127.0.0.1:8317/` health evidence.

It clears proxy environment variables, permits HTTPS only, bounds redirects,
time, and file sizes, and accepts download URLs only beneath the approved
repository. The archive SHA-256 must independently match both the GitHub API
asset digest and the release's digest-verified `checksums.txt`. Archive and
extracted-binary hashes are never compared as though they were the same object.

The current binary is copied to a root-only backup before replacement. Service
startup is polled for up to 60 seconds. Candidate version, binary digest,
systemd activity, and the bounded HTTP response must all agree. Any failure
after the switch restores the previous binary under an independent 60-second
health budget. Transient connection failures during startup are suppressed;
only terminal success, successful rollback, or failed rollback is reported.

## One-time installation

Review both files, transfer them through the existing Tailnet administration
path, and run the installer once from a provider recovery console:

```bash
bash /tmp/install-cliproxyapi-updater.sh /tmp/lodge-upgrade-cliproxyapi
```

The installer validates the helper, creates a sudoers candidate with `visudo`,
installs both files, restores the prior pair on any post-install failure,
validates the complete sudoers set, and proves that the exact no-argument
command is authorized while an extra argument is denied.

After this bootstrap, routine upgrades no longer require the provider console.
An operator or Codex can use the fixed command over Tailnet SSH and then verify
the systemd process and loopback API independently.

## Explicit tradeoff

This policy tracks the upstream stable `latest` release. That is a conscious
supply-chain authorization: compromise of the approved upstream GitHub
repository could publish a malicious asset whose API digest and checksums agree.
The helper protects against transport corruption, wrong assets, mutable caller
input, partial installs, and unhealthy releases; it cannot protect against a
compromised upstream maintainer. Environments that require release-by-release
approval should use a root-pinned version and digest instead of this channel.

This capability is intentionally CLIProxyAPI-specific. It is not a generic
package manager or precedent for arbitrary root scripts. A future Web action
must invoke the same no-input transaction through the Agent's typed policy and
durable Hub audit; it must not expose a command field.
