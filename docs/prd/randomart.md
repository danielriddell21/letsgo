# PRD: Randomart fingerprint

| | |
|---|---|
| Issue | [#34](https://github.com/danielriddell21/letsgo/issues/34) |
| Epic | [#42](https://github.com/danielriddell21/letsgo/issues/42) |
| HLD | [hld/randomart.md](../hld/randomart.md) |
| PBS | [pbs/randomart.md](../pbs/randomart.md) |
| ADRs | [0014](../adr/0014-fingerprints-from-manifest-digest.md) |

## Problem Statement

Comparing two sha256 digests by eye is tedious, and people skip it. I want a
glanceable way to see that the release page and my own verify run describe
the same release.

## Solution

A drunken-bishop randomart (as in `ssh-keygen -lv`) of the manifest digest,
shown in a collapsed block in the release body and after a passing `verify`.

## User Stories

1. As a user, I want the release page to show a picture of the manifest digest, so that I can compare it at a glance.
2. As a user, I want `verify` to print the same picture, so that I can see the match.
3. As a user, I want no picture when verify fails, so that a bad release never looks familiar.
4. As a nerd, I want the standard OpenSSH algorithm, so that it's recognisable and testable.
5. As a maintainer, I want to be able to disable it, so that my release body stays as I want.

## Implementation Decisions

- The input is the manifest sha256, via a shared fingerprint block
  ([ADR-0014](../adr/0014-fingerprints-from-manifest-digest.md)).
- A 17×9 field, the OpenSSH alphabet, and an `[letsgo vX.Y.Z]` header
  truncated to fit.
- `internal/randomart` has no dependencies.
- The opt-out is `disable randomart` (#26), which drops the whole block.
- `TestManifestExcludesNotes` guards against the manifest containing the
  body.

## Testing Decisions

- Golden art for an ed25519 key's fingerprint, compared with `ssh-keygen -lv`.
- Edge cases: all-zero, all-`0xff`, and ending on `S`.

## Out of Scope

- Art in `plan` (the digest isn't final until the build).

## Further Notes

- It shares a block with #35's words.
