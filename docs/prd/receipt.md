# PRD: `verify --receipt`

| | |
|---|---|
| Issue | [#36](https://github.com/danielriddell21/letsgo/issues/36) |
| Epic | [#42](https://github.com/danielriddell21/letsgo/issues/42) |
| HLD | [hld/receipt.md](../hld/receipt.md) |
| PBS | [pbs/receipt.md](../pbs/receipt.md) |
| ADRs | [0014](../adr/0014-fingerprints-from-manifest-digest.md), [0015](../adr/0015-easter-eggs-render-verify-result.md) |

## Problem Statement

Verification output is correct but dry. A hidden, nerdy easter egg that is
still a faithful verification report makes letsgo memorable and gets people
talking about verifying releases.

## Solution

A hidden `verify --receipt` flag prints the result as a 40-column till
receipt. Each artifact is a line item with its size, short digest and status.
It shows totals, "odds of an accidental match: 1 in 2²⁵⁶", a Code 128
barcode of the manifest digest, and a PGP-word REF line. A failed verify
prints a VOID stamp.

## User Stories

1. As a curious user, I want a hidden `--receipt` flag, so that I can discover a fun output.
2. As a user, I want every artifact as a line item with ✓ MATCH or ✗ MISMATCH, so that the receipt is a real report.
3. As a user, I want a VOID stamp on failure, so that a bad release is unmistakable.
4. As a user, I want skipped checks shown, so that nothing is hidden.
5. As a user, I want a barcode and REF line derived from the manifest digest, so that the receipt carries the fingerprint.
6. As a user, I want plain output when piped or under `NO_COLOR`, so that it works everywhere.
7. As a maintainer, I want the receipt's result always equal to verify's, so that the egg can never lie.

## Implementation Decisions

- It is a renderer over `verify.Result`, with the same exit code
  ([ADR-0015](../adr/0015-easter-eggs-render-verify-result.md)).
- The barcode and REF come from the manifest digest
  ([ADR-0014](../adr/0014-fingerprints-from-manifest-digest.md)).
- Packages: `internal/receipt` and `internal/receipt/code128`.
- Fixed at 40 columns, with long names truncated in the middle.
- The flag is left out of `--help`.

## Testing Decisions

- Golden receipts for pass, fail, skip and a long name.
- Code 128 check-character and start/stop tests.
- `TestReceiptMatchesVerify`: the total is ✓ exactly when `Result.OK()` is
  true.

## Out of Scope

- A file output mode.
- Randomly printing a receipt unprompted.

## Further Notes

- Open questions: is "LETSGO MARKET" the shop name? A README footnote hint,
  or fully secret?
