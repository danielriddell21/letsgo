# ADR-0014: Human fingerprints derive only from the manifest digest

- Status: proposed
- Date: 2026-09-24
- Issues: [#34](https://github.com/danielriddell21/letsgo/issues/34), [#35](https://github.com/danielriddell21/letsgo/issues/35), [#36](https://github.com/danielriddell21/letsgo/issues/36)
- HLDs: [randomart](../hld/randomart.md), [pgp-words](../hld/pgp-words.md), [receipt](../hld/receipt.md)

## Context

Three features give people a human-friendly way to compare releases:
randomart, PGP words, and a receipt's barcode and REF line. Each could hash
something different: the tag, individual artifacts, or the manifest.

## Decision

- Every fingerprint is derived from **sha256 of the published `letsgo.json`
  bytes**, the digest already listed in `SHA256SUMS`.
- They share one block in the release body (randomart, then the words), shown
  only when verify passes. `disable randomart` (#26) drops the whole block.
- Only standard algorithms are used: drunken bishop (OpenSSH) and the PGP word
  list (1995).
- The manifest must never contain the release body
  (`TestManifestExcludesNotes`).

## Consequences

- The manifest names every artifact digest, so one fingerprint covers the
  whole release.
- The page, `verify`, the words and the receipt all agree, because they share
  one input.
- A wrong fingerprint is never shown: a failed verify prints none.

## Alternatives considered

- **A fingerprint per artifact.** Rejected: too many to compare.
- **Emoji or BIP-39.** Rejected: rendering varies, and there's no even/odd
  error signal.
