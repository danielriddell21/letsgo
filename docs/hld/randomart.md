# HLD: randomart fingerprint

Status: proposal. Nothing here is implemented.

| | |
|---|---|
| Issue | [#34](https://github.com/danielriddell21/letsgo/issues/34) |
| Epic | [#42](https://github.com/danielriddell21/letsgo/issues/42) |
| PRD | [prd/randomart.md](../prd/randomart.md) |
| PBS | [pbs/randomart.md](../pbs/randomart.md) |
| ADRs | [ADR-0014](../adr/0014-fingerprints-from-manifest-digest.md) |

A visual fingerprint of `sha256(letsgo.json)`, so people can compare releases,
or a release page and their own verify run, at a glance. It is the same idea
as `ssh-keygen -lv`: nerdy, integrity-related, and an easter egg.

## Algorithm

It is **drunken bishop**, as in OpenSSH's `sshkey_fingerprint_randomart`:

- a 17×9 field; the bishop starts in the centre;
- each byte of the digest gives four moves of two bits each, least
  significant pair first (`00` ↖, `01` ↗, `10` ↙, `11` ↘), with the bishop
  sliding along walls;
- each cell counts its visits, rendered with ` .o+=*BOX@%&#/^`;
- `S` marks the start and `E` the end.

Input: the 32 bytes of the manifest sha256, the same digest as in
`SHA256SUMS`. OpenSSH also walks a 32-byte SHA256 digest, so the same bytes
give the same picture as ssh-keygen would draw for them.

## Where

1. **Release body:** a collapsed block at the end, after #33's "what shipped"
   section:

   ````markdown
   <details><summary>Manifest fingerprint</summary>

   ```
   +--[letsgo v1.3.0]--+
   |      .o+.         |
   |     . =o+         |
   |      *.B .        |
   |     o.O.S         |
   |      =.*          |
   |     . =.E         |
   |                   |
   |                   |
   |                   |
   +----[SHA256]-------+
   sha256:ab12…
   ```

   aardvark adroitness … (#35)
   </details>
   ````

2. **`letsgo verify`:** prints the same art after a pass. Nothing is printed
   on a failure.

### Chicken and egg

The body contains the manifest digest, so the manifest must not contain the
body. It doesn't today. A test holds that: `TestManifestExcludesNotes`.

## Header

`[letsgo vX.Y.Z]`, truncated to fit the 17-column frame: `[letsgo v1.3.0]`
fits, and `[v10.20.30-rc.12]` drops the `letsgo`. In a monorepo (#24) the
prefix is dropped, and the tag is shown below the frame.

## Implementation

- `internal/randomart`: `Render(digest []byte, title string) string`, about
  60 lines with no dependencies.
- Golden tests: take an ed25519 public key, compute its SHA256 fingerprint,
  and render it. The output must match `ssh-keygen -lv` for that key, captured
  once and committed. Add edge cases: all-zero, all-`0xff` (wall sliding),
  and a digest that ends on `S`.
- `--snapshot`: `release --snapshot` shows the block. `plan` doesn't, because
  the digest isn't final until the build.

## Opt-out

`disable randomart` (#26) drops the whole fingerprint block (art and words)
from the body. `verify` output is unaffected.

## With other proposals

- **#35:** the PGP words sit under the art in the same block.
- **#36:** the receipt uses the barcode and REF line instead of the art, to
  keep a 40-column layout.

## Delivery

Phases, in order. Each is a vertical slice: a thin path through every layer, verified end to end. The behaviour each phase must meet is specified in the [PBS](../pbs/randomart.md).

1. Tracer bullet: `internal/randomart` and `verify` (user stories 2, 3, 4)
2. Release body (user stories 1)
3. Opt-out (user stories 5)
