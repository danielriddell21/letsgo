# HLD: `letsgo audit`

Status: proposal. Nothing here is implemented.

| | |
|---|---|
| Issue | [#30](https://github.com/danielriddell21/letsgo/issues/30) |
| Epic | [#41](https://github.com/danielriddell21/letsgo/issues/41) |
| PRD | [prd/audit.md](../prd/audit.md) |
| PBS | [pbs/audit.md](../pbs/audit.md) |
| ADRs | [ADR-0012](../adr/0012-audit-results-append-only.md) |

## Problem

The vulnerability gate (`internal/gate/vulncheck.go`) runs once, at release
time. A vulnerability disclosed afterwards is invisible: the manifest still
says `vulncheck: Pass`, and nothing re-checks the binaries people already
installed.

## Proposal

`letsgo audit [tag]` re-runs govulncheck against a shipped release's
**source**, using **today's** vulndb, and records a dated result on the
release.

### Scope

- No argument: the newest non-retracted **stable** release of each major.
- `<tag>`: that release only.

### Steps

1. Fetch `letsgo.json` and the source archive, and check the archive digest
   against the manifest. This reuses verify's path (`verify/rebuild.go`).
2. Extract the archive, then run govulncheck with the manifest's build tags
   and targets, reachability included, just as the gate does
   (`gate.Vulncheck`).
3. Append the result to `audit.json` on the release:

```json
{"schema": 1, "tag": "v1.2.0", "audits": [
  {"at": "2026-09-24T03:17:00Z", "vulndb": "2026-09-23", "govulncheck": "v1.1.4",
   "status": "affected",
   "findings": [{"id": "GO-2026-1234", "module": "golang.org/x/net", "fixed": "v0.31.0"}]}
]}
```

- **Append-only:** each run adds an entry, so the history is the record.
- **No churn:** a run with the same vulndb date and the same findings as the
  last entry adds nothing, and the asset isn't touched.
- **Exit code:** 0 whether or not there are findings, because findings are
  data. It fails only when the audit couldn't run (no tool, no source
  archive, or a digest mismatch, which is itself an integrity alarm).

### letsgo-action

Adds `command: audit`. Documented workflow:

```yaml
on: { schedule: [{ cron: "17 3 * * *" }] }
jobs:
  audit:
    runs-on: ubuntu-latest
    permissions: { contents: write }
    steps:
      - uses: danielriddell21/letsgo-action@v1
        with: { command: audit }
```

### Consumers

`letsgo verify <tag>` prints the latest entry as an informational line,
without changing the result:

```
  · audit   affected by GO-2026-1234 (as of 2026-09-24)
```

## Integrity notes

- `audit.json` is written after release, so it is **not** in `SHA256SUMS` or
  the manifest. `comparePublished` must list it as a known extra asset, not an
  unexpected one.
- An audit never touches the release's own assets. Its only write is
  `audit.json`.

## Out of scope

- Opening issues, editing release bodies, and warnings in selfupdate. All of
  them could read `audit.json` later.

## Open questions

- **Immutable releases** block adding assets after publishing. Options: keep
  results on an `audit` branch (`<tag>.json`), or skip immutable releases with
  a message. Proposed: the branch, via the contents API, which works for both.
- Does "each major" include `v0`? Proposed: yes, as its own line.

## Delivery

Phases, in order. Each is a vertical slice: a thin path through every layer, verified end to end. The behaviour each phase must meet is specified in the [PBS](../pbs/audit.md).

1. Tracer bullet: `letsgo audit <tag>` (user stories 3, 8)
2. Record (user stories 5, 6, 7)
3. Scope and schedule (user stories 1, 2)
4. Consumers (user stories 4)
