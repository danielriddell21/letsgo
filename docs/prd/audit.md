# PRD: `letsgo audit`

| | |
|---|---|
| Issue | [#30](https://github.com/danielriddell21/letsgo/issues/30) |
| Epic | [#41](https://github.com/danielriddell21/letsgo/issues/41) |
| HLD | [hld/audit.md](../hld/audit.md) |
| PBS | [pbs/audit.md](../pbs/audit.md) |
| ADRs | [0012](../adr/0012-audit-results-append-only.md) |

## Problem Statement

My release passed the vulnerability gate on the day it shipped. A month
later a vulnerability is disclosed in a dependency it uses, and nothing tells
me or my users. The manifest still says `vulncheck: Pass`.

## Solution

`letsgo audit` re-runs govulncheck against the source of shipped releases
using today's vulndb, and appends a dated result to `audit.json` on the
release. letsgo-action can run it on a schedule. `verify` shows the latest
audit entry.

## User Stories

1. As a maintainer, I want a nightly audit of my supported releases, so that I learn about new vulnerabilities quickly.
2. As a maintainer, I want the audit to cover the newest stable release of each major, so that supported lines are checked without noise.
3. As a maintainer, I want `letsgo audit v1.2.0` to check one release, so that I can investigate.
4. As a consumer, I want to see "affected by GO-XXXX as of <date>" when I verify, so that I know before trusting a binary.
5. As a consumer, I want the audit history kept, so that I can tell when a release became affected.
6. As a maintainer, I want unchanged results not to rewrite the file, so that there's no churn.
7. As a maintainer, I want findings not to fail the scheduled run, so that red runs mean the audit itself broke.
8. As a security reviewer, I want the audit to use the release's exact source archive, checked against the manifest, so that it audits what shipped.

## Implementation Decisions

- `audit.json` is append-only and outside `SHA256SUMS` and the manifest
  ([ADR-0012](../adr/0012-audit-results-append-only.md)).
- It reuses verify's source fetch and digest check, and `gate.Vulncheck` with
  the manifest's tags and targets.
- letsgo-action gains `command: audit`.

## Testing Decisions

- A fake forge and fake govulncheck cover append, dedupe and history
  ordering.
- `verify` ignores `audit.json` as an unexpected asset and prints its latest
  entry.
- An archive digest mismatch fails the audit.

## Out of Scope

- Opening issues, editing release bodies, and selfupdate warnings.

## Further Notes

- Immutable releases: the proposed fallback is an `audit` branch with
  `<tag>.json`.
