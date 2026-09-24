# PRD: `verify --words` (PGP word list)

| | |
|---|---|
| Issue | [#35](https://github.com/danielriddell21/letsgo/issues/35) |
| Epic | [#42](https://github.com/danielriddell21/letsgo/issues/42) |
| HLD | [hld/pgp-words.md](../hld/pgp-words.md) |
| PBS | [pbs/pgp-words.md](../pbs/pgp-words.md) |
| ADRs | [0014](../adr/0014-fingerprints-from-manifest-digest.md) |

## Problem Statement

When two people check a release together over a call, reading hex aloud is
error-prone, and they give up after a few characters. letsgo's promise is
that anyone can check a release, so checking it together should be easy.

## Solution

`verify --words` prints the full manifest digest as 32 words from the PGP
word list. Alternating two- and three-syllable words make a dropped, repeated
or swapped word audible. The same words appear in the release body's
fingerprint block.

## User Stories

1. As a user on a call, I want the digest as words, so that I can read it aloud reliably.
2. As a listener, I want a dropped, repeated or swapped word to be noticeable, so that errors are caught.
3. As a user, I want all 32 words, so that the spoken check is as strong as the written one.
4. As a user, I want numbered rows of 4, so that "line 5" is easy to say.
5. As a user, I want no words on a failed verify, so that nobody reads out a bad hash.
6. As a user, I want the same words on the release page, so that I can compare against it.

## Implementation Decisions

- The input is the manifest sha256
  ([ADR-0014](../adr/0014-fingerprints-from-manifest-digest.md)).
- `internal/pgpwords.Encode([]byte) []string`, with two 256-word tables.
- The words are printed with `--no-rebuild` too, since the digest check
  doesn't depend on the rebuild.
- `verify --json` gains `manifest_words`.

## Testing Decisions

- Golden tests against the published list: first and last entries, a known
  20-byte PGP fingerprint, and alternation over 32 bytes.

## Out of Scope

- A read-back mode (`--words "…"`).
- `--say` text-to-speech.

## Further Notes

- #36's REF line uses the first 4 words.
