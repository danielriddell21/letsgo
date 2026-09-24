# PBS: Checksum database cross-check

> Product-based specification: what the product must do, stated so it can be tested.
> [PRD](../prd/sumdb.md) · [HLD](../hld/sumdb.md) · Issue [#32](https://github.com/danielriddell21/letsgo/issues/32) · ADR [0013](../adr/0013-sumdb-check-against-proxy-zip.md)

## Scope

After publishing, check that sum.golang.org and the module proxy agree with
the release's source archive.

## Interfaces

| surface | specification |
| --- | --- |
| trigger | `release`, after the proxy warm |
| sumdb | `GET <sumdb>/lookup/<escaped module>@<version>` |
| proxy | `GET <proxy>/<escaped module>/@v/<version>.zip` |
| output | `✓ sum.golang.org agrees with the source archive`, a `✗` mismatch with a file list, or `· skipped: <reason>` |

## Requirements

| ID | requirement | story |
| --- | --- | --- |
| SD-1 | The check MUST run after the proxy warm, on every non-snapshot, non-draft release. | 1 |
| SD-2 | The proxy zip's `h1:` hash MUST equal sumdb's `h1:` line for the module. | 1 |
| SD-3 | Every file in the module zip MUST be byte-identical to the same path in the source archive. | 1 |
| SD-4 | On a mismatch, the output MUST list each differing or missing file, suggest `letsgo yank`, and exit non-zero. | 2, 3 |
| SD-5 | Files in the archive that aren't in the zip MUST be listed as informational, not failed. | — |
| SD-6 | When sumdb has no record yet, the check MUST retry with backoff (up to ~60s), then print `!` with a manual command. | 5 |
| SD-7 | The check MUST be skipped with a reason when the module matches `GOPRIVATE`, `GONOSUMDB` or `GONOSUMCHECK`, or the proxy warm is disabled. | 4 |

## Acceptance scenarios

1. **Given** a matching release, **when** `release` finishes, **then** it prints `✓`. (SD-2, SD-3)
2. **Given** a source archive with one file altered after tagging, **when** the check runs, **then** it prints `✗`, names the file, and exits non-zero. (SD-4)
3. **Given** sumdb returns 404 twice and then the record, **when** the check runs, **then** it passes. (SD-6)
4. **Given** `GOPRIVATE=example.com/*`, **when** releasing `example.com/x`, **then** the check prints `skipped: private module`. (SD-7)
