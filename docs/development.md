# Development and delivery

## Toolchain

Use Go 1.25.12 or newer. This patch-level floor excludes known standard-library
vulnerabilities in earlier releases. CI uses the latest Go 1.26.x patch and
production artifacts must be built by the same supported toolchain family.

## Local checks

```bash
npm test
```

This is the same primary quality gate used by CI. Network-dependent vulnerability scanning runs in GitHub Actions with a pinned `govulncheck` version.

## Delivery workflow

Work proceeds in independently verifiable milestones:

1. define acceptance conditions and update the scorecard when a metric changes;
2. implement one vertical slice across API, storage, UI, tests, and docs;
3. run local quality gates;
4. inspect the exact diff and stage only milestone-owned files;
5. create a terse, scoped commit;
6. push the `codex/lodge-long-roadmap` branch;
7. verify GitHub CI before declaring the milestone complete.

Unrelated or pre-existing working-tree files are never staged automatically.

## Commit boundaries

A commit should be reversible and explain one coherent outcome. Database migrations, generated assets, and documentation required by the outcome belong in the same commit. Unrelated cleanup does not.

## Real-server changes

Repository delivery and live fleet changes are separate transactions. Any SSH, firewall, Tailscale, systemd, or deployment change requires:

- a read-only preflight;
- a tested recovery path;
- a second access session where lockout is possible;
- one host at a time;
- post-change evidence;
- an entry in the operations log.
