# PBS: `letsgo audit`

> Product-based specification: what the product must do, stated so it can be tested.
> [PRD](../prd/audit.md) · [HLD](../hld/audit.md) · Issue [#30](https://github.com/danielriddell21/letsgo/issues/30) · ADR [0012](../adr/0012-audit-results-append-only.md)

## Scope

Re-checking shipped releases against the current vulnerability database, and
recording the results.

## Interfaces

| surface | specification |
| --- | --- |
| CLI | `letsgo audit [tag] [--repo owner/name] [--token]` |
| record | release asset `audit.json`: `{schema: 1, tag, audits: [{at, vulndb, govulncheck, status: "clean"\|"affected", findings: [{id, module, fixed}]}]}` |
| verify | an informational line: `audit  affected by <ids> (as of <date>)` or `clean (as of <date>)` |
| letsgo-action | `command: audit` |

## Requirements

| ID | requirement | story |
| --- | --- | --- |
| AU-1 | With no tag, audit MUST check the newest non-retracted stable release of each major version. | 2 |
| AU-2 | With a tag, audit MUST check only that release. | 3 |
| AU-3 | Audit MUST check the source archive's digest against the manifest before scanning, and Fail on a mismatch. | 8 |
| AU-4 | Audit MUST scan with the manifest's build tags and targets, reporting only reachable findings. | 8 |
| AU-5 | Audit MUST append an entry to `audit.json`, keeping all earlier entries. | 5 |
| AU-6 | Audit MUST NOT append when the vulndb date and the findings equal the last entry. | 6 |
| AU-7 | The exit code MUST be 0 whether or not there are findings, and non-zero only when the audit couldn't run. | 7 |
| AU-8 | Audit MUST NOT modify any other release asset. | — |
| AU-9 | `verify` MUST NOT count `audit.json` as an unexpected asset, and MUST NOT change its result based on it. | 4 |
| AU-10 | `verify` MUST print the latest audit entry when one exists. | 4 |

## Errors and edge cases

- govulncheck missing: exit non-zero (the audit didn't happen).
- A release with no manifest or no source archive (made before letsgo):
  skipped, with a reason.
- An immutable release: write to the `audit` branch as `<tag>.json` instead
  (proposed).

## Acceptance scenarios

1. **Given** releases `v1.3.2`, `v1.4.0` and `v2.0.1`, **when** `letsgo audit` runs, **then** only `v1.4.0` and `v2.0.1` are audited. (AU-1)
2. **Given** a planted vulnerable dependency that is reachable, **when** audit runs, **then** `audit.json` gains an `affected` entry and the exit is 0. (AU-5, AU-7)
3. **Given** the same state the next day with the same vulndb, **when** audit runs, **then** `audit.json` is unchanged. (AU-6)
4. **Given** a tampered source archive, **when** audit runs, **then** it exits non-zero and writes nothing. (AU-3)
5. **Given** an affected release, **when** `verify` runs, **then** it passes and prints the audit line. (AU-9, AU-10)
