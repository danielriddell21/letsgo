# HLD: reading the manifest digest aloud (`verify --words`)

Status: proposal. Nothing here is implemented.

| | |
|---|---|
| Issue | [#35](https://github.com/danielriddell21/letsgo/issues/35) |
| Epic | [#42](https://github.com/danielriddell21/letsgo/issues/42) |
| PRD | [prd/pgp-words.md](../prd/pgp-words.md) |
| PBS | [pbs/pgp-words.md](../pbs/pgp-words.md) |
| ADRs | [ADR-0014](../adr/0014-fingerprints-from-manifest-digest.md) |

## Problem

`verify` proves a release rebuilds, and the manifest digest is what two people
compare to know they checked the *same* release. On screen that is easy. Over
a call it is not: people read eight hex characters, then give up. letsgo's
claim is that anyone can check a release, so two people checking it together
should be easy too.

## Proposal

`letsgo verify --words` prints the sha256 of `letsgo.json` as the PGP word
list (Juola & Zimmermann, 1995), after a passing verify:

```
  v1.3.0 verified in 41s

  manifest sha256, read aloud:
   1 aardvark    adroitness   absurd      adviser
   5 accrue      aftermath    acme        aggregate
   …                                              (32 words)
```

### Why the PGP word list

- It is 256 two-syllable words for even byte positions and 256 three-syllable
  words for odd ones. A dropped, repeated or swapped word breaks the rhythm,
  so a listener hears the mistake instead of having to find it.
- The words were chosen to be phonetically distinct.
- It is a known standard, with published tables to test against, so nothing
  is invented here.

### Decisions

| choice | decision | why |
| --- | --- | --- |
| input | sha256 of the published `letsgo.json` bytes | the manifest names every artifact's digest, so it covers the release; it is already in `SHA256SUMS` |
| length | **all 32 bytes, so 32 words** | truncating would make the spoken check weaker than the written one; about 30 seconds to read |
| layout | numbered rows of 4 | "line 5" is something people can say |
| on failure | **no words** | nobody should read out a hash that did not verify |
| release body | yes, in the #34 fingerprint block under the randomart | the page and the terminal show the same thing |
| `--no-rebuild` | still printed on a pass | the manifest digest check doesn't depend on the rebuild |

### Implementation

- `internal/pgpwords`: `Encode([]byte) []string` holding two `[256]string`
  tables. It is a pure function with zero dependencies.
- Golden tests against the published list, covering the first and last
  entries of both tables, a known 20-byte PGP fingerprint, and even/odd
  alternation over a 32-byte input.
- `verify.Result` exposes the manifest digest; `cmd/letsgo` renders the rows.
- Release notes: the #34 renderer calls the same `Encode`.

## With other proposals

- **#34 randomart:** shares the fingerprint block and the input digest.
- **#36 receipt:** the `REF:` line is the first four words.
- **#26 features:** `disable randomart` also drops the words from the body,
  since both are one block. The `--words` flag is independent.
- **#28 `--json`:** `verify --json` gains `"manifest_words": [...]`.

## Alternatives

- **Hex in groups of 4.** Harder to say aloud, and a misheard character goes
  unnoticed.
- **Emoji (Signal/Telegram style).** Fun, but they render differently across
  terminals and can't be spoken unambiguously.
- **BIP-39.** A mnemonic for keys, with a checksum. It is built for writing a
  secret down, not for confirming a public value aloud, and has no even/odd
  error signal.

## Open questions

- A read-back mode (`--words "aardvark …"`) that compares what the other
  person read out? Deferred.
- `--say` via the OS text-to-speech? Deferred (egg territory).

## Delivery

Phases, in order. Each is a vertical slice: a thin path through every layer, verified end to end. The behaviour each phase must meet is specified in the [PBS](../pbs/pgp-words.md).

1. Tracer bullet: `internal/pgpwords` and `verify --words` (user stories 1, 2, 3, 4, 5)
2. Release body (user stories 6)
3. JSON
