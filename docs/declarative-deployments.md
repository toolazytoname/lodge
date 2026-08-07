# Declarative deployments

Lodge deployment policy is a release allowlist, not a remote Compose editor.
The browser will select a reviewed release ID; it cannot upload YAML, enter an
image, change a path, supply environment variables, or run a command.

## Version 1 scope

Version 1 manages one service in an existing Docker Compose project and requires
all of the following:

- the service is explicitly stateless;
- the project directory, Compose file, optional `.env`, and their path chain are
  root-owned, non-symlinked, and not group/world writable;
- every allowed image is pinned to a full lowercase sha256 repository digest;
- health is either Docker `healthy` or an HTTP 2xx response from an exact
  loopback URL;
- the currently running container has an immutable repository digest that can
  become the first rollback point.

Stateful services are intentionally rejected. Database backup is not equivalent
to switching an image, and Lodge will not infer a restore procedure.

## Host policy

Start from [`deploy/agent-deployments.example.json`](../deploy/agent-deployments.example.json).
Replace every example path, service, URL, release label, and digest with reviewed
host-specific values. Validate the Compose service and health endpoint directly
on the host before installing the policy.

Install it without printing secrets or making the configuration directory
writable by the `lodge` account:

```bash
sudo install -o root -g root -m 0600 reviewed-deployments.json \
  /etc/lodge-agent/deployments.json
sudo deploy/install-agent.sh /path/to/lodge-agent
```

The installer validates file ownership, policy grammar, Compose path ownership,
existing rollback state, exact sudoers entries, and the authenticated service
projection. An absent policy returns an empty deployment list.

## Execution and rollback

For a selected release, the root helper performs this fixed sequence:

1. re-read policy and rollback state under the shared operation lock;
2. validate root-owned paths and the registered Compose service;
3. inspect or pull the exact image digest;
4. discover the current immutable digest when no Lodge state exists;
5. generate and validate a temporary Compose override;
6. update only the registered service with `--no-deps`;
7. verify running state and the registered health check;
8. atomically commit current/previous state, or reapply and health-check the
   pre-operation image.

The durable file under `/var/lib/lodge-agent/deployments` is implementation
state, not an operator editing surface. Do not modify it manually. Preserve it
with the host recovery material; if it is absent or invalid Lodge refuses to
invent a rollback history.

## Acceptance

A stack is ready for Web exposure only when policy listing succeeds, the Agent
shows the expected release IDs without host paths, the current release has a
known immutable digest, the health check is deterministic, and a controlled
failure test has demonstrated recovery on a non-production fixture. Production
execution additionally requires Hub audit and exact confirmation; those are the
remaining M7 integration boundary.
