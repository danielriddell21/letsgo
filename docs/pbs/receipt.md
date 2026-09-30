# PBS: `verify --receipt`

> Product-based specification: what the product must do, stated so it can be tested.
> [PRD](../prd/receipt.md) · [HLD](../hld/receipt.md) · Issue [#36](https://github.com/danielriddell21/letsgo/issues/36) · ADRs [0014](../adr/0014-fingerprints-from-manifest-digest.md), [0015](../adr/0015-easter-eggs-render-verify-result.md)

## Scope

A hidden rendering of the verify result as a till receipt.

## Interfaces

| surface | specification |
| --- | --- |
| CLI | `letsgo verify [tag] --receipt` (not listed in `--help`) |
| layout | 40 columns: shop header; repo and tag; date/time and go version; one item per artifact (name, then size, short digest and status); ITEMS; BYTES DIFFERING; PROVENANCE; TOTAL; odds line; barcode; digest label; `REF:` line; footer |
| status values | `✓ MATCH`, `✗ MISMATCH`, `— SKIPPED` |
| total | `BIT-FOR-BIT ✓`, or `VOID` plus a `VOID — DO NOT ACCEPT` stamp |

## Requirements

| ID | requirement | story |
| --- | --- | --- |
| RC-1 | `--receipt` MUST be accepted by `verify` and MUST NOT appear in `--help`. | 1 |
| RC-2 | The receipt MUST be rendered from the same `verify.Result` as normal output, and the exit code MUST be identical. | 7 |
| RC-3 | TOTAL MUST read `BIT-FOR-BIT ✓` if and only if `Result.OK()`. | 7 |
| RC-4 | Every artifact MUST appear as a line item with its status. | 2 |
| RC-5 | Skipped checks MUST appear as `— SKIPPED`. | 4 |
| RC-6 | On failure, TOTAL MUST read `VOID` and the stamp MUST be printed. | 3 |
| RC-7 | The barcode MUST be a valid Code 128-B encoding of the first 12 hex characters of the manifest digest. | 5 |
| RC-8 | `REF:` MUST show the first 4 PGP words of the manifest digest (#35). | 5 |
| RC-9 | No line may exceed 40 display columns; long names are truncated in the middle with `…`. | — |
| RC-10 | Output MUST contain no ANSI codes when stdout isn't a terminal or `NO_COLOR` is set. | 6 |

## Acceptance scenarios

1. **Given** a passing fixture, **when** `verify --receipt` runs, **then** the output equals the golden pass receipt and exits 0. (RC-2, RC-3)
2. **Given** one mismatched artifact, **when** it runs, **then** that line shows `✗ MISMATCH`, TOTAL is `VOID`, the stamp is printed, and it exits non-zero. (RC-6)
3. **Given** every `Result` fixture, **when** rendered, **then** TOTAL is ✓ exactly when `Result.OK()` is true. (RC-3)
4. **Given** `letsgo verify --help`, **when** it's printed, **then** `receipt` doesn't appear. (RC-1)
