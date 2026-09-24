# PBS: Randomart fingerprint

> Product-based specification: what the product must do, stated so it can be tested.
> [PRD](../prd/randomart.md) · [HLD](../hld/randomart.md) · Issue [#34](https://github.com/danielriddell21/letsgo/issues/34) · ADR [0014](../adr/0014-fingerprints-from-manifest-digest.md)

## Scope

A drunken-bishop picture of the manifest digest, in the release body and in
`verify`.

## Interfaces

| surface | specification |
| --- | --- |
| input | the 32-byte sha256 of the published `letsgo.json` |
| art | a 17×9 field, alphabet ` .o+=*BOX@%&#/^`, `S` start, `E` end, header `[letsgo vX.Y.Z]` (truncated to fit), footer `[SHA256]` |
| release body | a collapsed "Manifest fingerprint" block: the art, then `sha256:<hex>` (and #35's words) |
| verify | the art printed after a pass |
| config | `disable randomart` (#26) |

## Requirements

| ID | requirement | story |
| --- | --- | --- |
| RA-1 | The walk MUST follow OpenSSH's `fingerprint_randomart`: 2-bit moves, least significant pair first, sliding along walls. | 4 |
| RA-2 | The field (the 9 rows between the frame lines) MUST be byte-identical to `ssh-keygen -lv` for the same SHA256 digest bytes; only the frame titles differ. | 4 |
| RA-3 | The release body MUST contain the block, after the "What shipped" section. | 1 |
| RA-4 | `verify` MUST print the art after a pass. | 2 |
| RA-5 | `verify` MUST NOT print art on a failure. | 3 |
| RA-6 | The manifest MUST NOT contain the release body. | — |
| RA-7 | `disable randomart` MUST remove the whole fingerprint block from the body, and MUST NOT affect `verify` output. | 5 |
| RA-8 | `release --snapshot` MUST show the block; `plan` MUST NOT. | — |

## Acceptance scenarios

1. **Given** an ed25519 test key's SHA256 fingerprint, **when** it's rendered, **then** the field equals the committed `ssh-keygen -lv` golden file. (RA-2)
2. **Given** a released fixture, **when** `verify` passes, **then** its art equals the art in the release body. (RA-3, RA-4)
3. **Given** a mismatched artifact, **when** `verify` runs, **then** no art is printed. (RA-5)
