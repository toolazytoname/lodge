# Quality gates

Lodge uses three kinds of evidence. A single aggregate score is intentionally avoided because it can hide a critical security failure behind unrelated passing metrics.

## 1. Merge gates

Every commit intended for push must pass `npm test`, which currently enforces:

- Go formatting;
- `go vet`;
- build of every Go package;
- unit and integration tests;
- the Go race detector;
- shell syntax for deployment scripts;
- generated TypeScript API-contract drift and strict browser compilation;
- JSON validity of the quality scorecard;
- whitespace checks.

GitHub CI repeats these checks on Go 1.26 and Node 24, installs frontend build
dependencies with `npm ci`, and runs `govulncheck`. Local development requires
Go 1.25.12 or newer, matching the supported floor declared in `go.mod`.

## 2. Runtime targets

The machine-readable baseline lives in [`quality/scorecard.json`](../quality/scorecard.json). Runtime metrics are measured from real or sanitized observations, never invented to make a milestone appear complete.

Initial service-level targets:

| Metric | Target |
| --- | ---: |
| Managed hosts | 5/5 |
| Normal observation age | <= 60 seconds |
| Host offline detection | <= 90 seconds |
| New public listener detection | <= 90 seconds |
| SSH failure-spike detection | <= 90 seconds |
| Known workload attribution | >= 95% |
| Registered Web link success | >= 95% |
| Duplicate notifications for one event | 0 |
| Operations with an audit record | 100% |
| Public management endpoints | 0 |

Alert thresholds will be calibrated against two weeks of real data. Detection latency and duplicate suppression remain hard acceptance criteria during calibration.

## 3. Human acceptance

UI and architecture require structured human review in addition to automation.

Each core page is reviewed from 1 to 5 for:

- information hierarchy;
- consistency;
- readability;
- feedback and error recovery;
- visibility of risk;
- density and relevance;
- owner preference.

No category may be below 4 for a milestone to be considered visually accepted. Core flows must work at 390px mobile width and at desktop widths from 1280px through 1920px.

Architecture is accepted through change scenarios: adding a collector, notification channel, storage implementation, authentication method, or workload kind must not require unrelated packages to change.

## Definition of done

A feature is complete only when:

1. acceptance conditions are explicit;
2. API and persistence changes are versioned;
3. normal, loading, empty, offline, partial-failure, and error states are handled where applicable;
4. critical behavior is tested at the lowest useful level;
5. security and privacy impact is reviewed;
6. documentation and scorecard are updated;
7. a real machine or sanitized fixture validates the behavior;
8. the milestone quality gates pass.
