# PBS: `verify --words`

> Product-based specification: what the product must do, stated so it can be tested.
> [PRD](../prd/pgp-words.md) · [HLD](../hld/pgp-words.md) · Issue [#35](https://github.com/danielriddell21/letsgo/issues/35) · ADR [0014](../adr/0014-fingerprints-from-manifest-digest.md)

## Scope

The manifest digest encoded as PGP words, for comparing aloud.

## Interfaces

| surface | specification |
| --- | --- |
| input | the 32-byte sha256 of the published `letsgo.json` |
| CLI | `letsgo verify [tag] --words` |
| output | `manifest sha256, read aloud:`, then 8 numbered rows of 4 words (row numbers 1, 5, 9, …) |
| release body | the words under the art in the fingerprint block (#34) |
| JSON | `verify --json` field `manifest_words: [32 strings]` (#28) |

## Requirements

| ID | requirement | story |
| --- | --- | --- |
| PW-1 | Byte *i* MUST be encoded from the even (two-syllable) list when *i* is even, and from the odd (three-syllable) list when *i* is odd. | 2 |
| PW-2 | The tables MUST equal the published PGP word list. | 2 |
| PW-3 | All 32 bytes MUST be encoded, with no truncation. | 3 |
| PW-4 | Words MUST be printed as 8 numbered rows of 4. | 4 |
| PW-5 | Words MUST be printed only after a passing verify, including under `--no-rebuild`. | 5 |
| PW-6 | The release body words MUST equal the `verify --words` output for the same release. | 6 |

## Acceptance scenarios

1. **Given** byte `0x00` at position 0 and at position 1, **when** encoded, **then** the results are `aardvark` and `adroitness`. (PW-1, PW-2)
2. **Given** byte `0xFF` at positions 0 and 1, **when** encoded, **then** the results are `Zulu` and `Yucatan`. (PW-2)
3. **Given** a released fixture, **when** `verify --words` passes, **then** it prints 32 words matching the release body. (PW-3, PW-6)
4. **Given** a failing verify, **when** `--words` is passed, **then** no words are printed. (PW-5)
