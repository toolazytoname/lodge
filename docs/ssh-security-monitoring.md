# SSH security monitoring

## What Lodge detects

Agent 0.5.0 reads the most recent ten minutes of systemd-journald OpenSSH
records through a fixed root helper. Lodge counts these failure forms:

- failed password;
- failed public key;
- failed keyboard-interactive/PAM;
- maximum authentication attempts exceeded.

The Hub opens one host-scoped `ssh.bruteforce` event at either 30 failures in
the window or 10 from one source. It escalates to critical at 100 total or 50
from one source. The event remains active until the rolling window drops below
both 10 total and three from every source, preventing threshold flapping.

With the normal 30-second Hub scrape, a qualifying journal record should appear
within 90 seconds. This is a delivery target, not proof until a live synthetic
failure test records the measured latency.

## What leaves the server

Only the UTC window, total count, and at most 20 canonical source IP/count pairs
leave the root helper. Lodge does not collect usernames, accepted-login events,
ports, commands, authentication material, raw journal messages, or arbitrary
journal fields. The event UI and Webhook detail show at most the leading three
sources.

A source IP answers “which network address produced these failures,” not “who
is the attacker.” NAT, proxies, compromised hosts, and address reassignment make
human attribution a separate investigation.

## Coverage and failure behavior

The collector requires systemd-journald records tagged with `_COMM=sshd` or
`SYSLOG_IDENTIFIER=sshd`. It does not currently cover Dropbear, containerized
SSH servers, auth.log-only systems, cloud-provider login gateways, or message
formats outside the tested OpenSSH patterns.

If journal access, parsing, time bounds, or source validation fails, the Agent
adds a collection warning and omits the SSH summary. The Hub keeps any existing
SSH event active; missing telemetry never means recovery. The installer rejects
an upgrade whose real service-context status lacks a valid SSH summary.

## Safe live acceptance

Use a test source you control and keep a second recovery session open. Generate
failed logins against a non-existent test username without placing a password in
shell history. Verify:

1. the Agent status reports an increased failure total and the expected source
   IP, with no username or raw message;
2. the Hub creates one event only after the documented threshold;
3. the event appears within 90 seconds, can be acknowledged without resolving,
   and later resolves after the quiet-window threshold;
4. a configured receiver deduplicates `X-Lodge-Delivery` if Webhook is enabled;
5. SQLite contains the aggregate/event but no test username or raw journal line.

Do not weaken SSH or firewall configuration merely to create acceptance data.
If a safe controlled source is unavailable, deploy the read-only collector and
record detection latency as pending rather than simulating production evidence.
