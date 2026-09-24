# HLD: `verify --receipt`

Status: proposal. Nothing here is implemented.

| | |
|---|---|
| Issue | [#36](https://github.com/danielriddell21/letsgo/issues/36) |
| Epic | [#42](https://github.com/danielriddell21/letsgo/issues/42) |
| PRD | [prd/receipt.md](../prd/receipt.md) |
| PBS | [pbs/receipt.md](../pbs/receipt.md) |
| ADRs | [ADR-0014](../adr/0014-fingerprints-from-manifest-digest.md), [ADR-0015](../adr/0015-easter-eggs-render-verify-result.md) |

An easter egg that is still a faithful report: `verify`'s result printed as a
till receipt. Each artifact is a line item, the total is "bit-for-bit", and a
failure gets a VOID stamp.

## Shape

```
        ╔═══════════════════════════════╗
        ║        LETSGO  MARKET         ║
        ╚═══════════════════════════════╝
  you/gambit                       v1.3.0
  2026-09-24 14:02            go1.26.2
  --------------------------------------
  gambit_linux_amd64.tar.gz
    8.4 MB   ab12cd34…ef56      ✓ MATCH
  gambit_darwin_arm64.tar.gz
    8.1 MB   77e0a1f2…09bc      ✓ MATCH
  source.tar.gz
    1.2 MB   c0ffee12…3456      ✓ MATCH
  --------------------------------------
  ITEMS                               3
  BYTES DIFFERING                     0
  PROVENANCE                  ✓ ATTESTED
  --------------------------------------
  TOTAL                   BIT-FOR-BIT ✓
  odds of an accidental match: 1 in 2²⁵⁶

  ▌▐█▌▐▐█▌█▐▌▐█▐▌█▌▐▐▌█▐█▌▐▌█▐▐█▌▐█▌▐
          ab12cd34ef56 (manifest)
  REF: aardvark adroitness absurd adviser

      thank you for verifying. come again
```

A failure:

```
  gambit_linux_amd64.tar.gz
    8.4 MB   ab12cd34…ef56    ✗ MISMATCH
  --------------------------------------
  TOTAL                            VOID
        ┌───────────────────────┐
        │  VOID — DO NOT ACCEPT │
        └───────────────────────┘
```

## Rules

- **Hidden.** `--receipt` is registered but left out of `--help`. That needs
  a small change to how `verify`'s usage is printed, or the flag is read
  before `flag.Parse`.
- **The same checks, the same exit code.** It is a renderer over
  `verify.Result`, and never a separate path. A receipt that says ✓ where
  `verify` says ✗ is impossible by construction.
- **Every row maps to a check.** Artifacts come from the rebuild compare,
  PROVENANCE from `checkProvenance`, and images get their own rows. Skipped
  checks print `— SKIPPED`, so they are never hidden.
- **Fixed width of 40 columns.** It never wraps. Long names are truncated
  in the middle (`gambit_li…amd64.tar.gz`).
- **Plain when piped or under `NO_COLOR`.** The box-drawing characters stay,
  since they are text rather than colour.
- **BYTES DIFFERING** is the count of artifacts whose rebuilt digest differs.
  Byte-level diffs aren't computed, and the label says bytes only because a
  receipt should.

## Pieces

| piece | source |
| --- | --- |
| header: repo, tag, time, go | `Result`, `Manifest.Builder.Go`, wall clock (the receipt is not part of any release, so time is fine) |
| line items | the artifacts in `Result`: name, size, short digest, status |
| barcode | **Code 128-B** of the first 12 hex characters of the manifest digest, drawn with `▌▐█` half-blocks. It is decorative, with no promise that it scans from a terminal. The encoding is real and golden-tested |
| REF | the first 4 words from `internal/pgpwords` (#35) |
| odds | constant `2²⁵⁶`: the chance of an accidental sha256 match, not a security claim beyond that |

Code: `internal/receipt` (render) and `internal/receipt/code128`. No
dependencies.

## Tests

- Golden receipts for pass, fail, skip, and a long name.
- Code 128: the check character, and the start and stop patterns, against
  known encodings.
- `TestReceiptMatchesVerify`: for every `Result` fixture, the receipt's total
  is ✓ exactly when `Result.OK()` is true.

## Open questions

- Shop name: "LETSGO MARKET"?
- A README footnote hint, or fully secret?

## Delivery

Phases, in order. Each is a vertical slice: a thin path through every layer, verified end to end. The behaviour each phase must meet is specified in the [PBS](../pbs/receipt.md).

1. Tracer bullet: the receipt without extras (user stories 1, 2, 3, 4, 6, 7)
2. Barcode and REF (user stories 5)
