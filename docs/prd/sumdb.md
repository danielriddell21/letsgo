# PRD: Checksum database cross-check

| | |
|---|---|
| Issue | [#32](https://github.com/danielriddell21/letsgo/issues/32) |
| Epic | [#41](https://github.com/danielriddell21/letsgo/issues/41) |
| HLD | [hld/sumdb.md](../hld/sumdb.md) |
| PBS | [pbs/sumdb.md](../pbs/sumdb.md) |
| ADRs | [0013](../adr/0013-sumdb-check-against-proxy-zip.md) |

## Problem Statement

letsgo proves my binaries rebuild from the source archive, but most of my
users get the source through `go get`, which trusts sum.golang.org. If the
tag moved, or the archive came from a different tree, the two would differ,
and nobody would notice.

## Solution

After the proxy warm, letsgo fetches the sumdb hash for `module@version`,
downloads the module zip, checks its hash, and compares it file by file with
the release's source archive. A mismatch is a loud alarm. Private modules are
skipped.

## User Stories

1. As a maintainer, I want confirmation that sum.golang.org agrees with my source archive, so that `go get` users get what I released.
2. As a maintainer, I want a mismatch to name the differing files, so that I can see what went wrong.
3. As a maintainer, I want a mismatch to fail the run loudly and suggest `letsgo yank`, so that I act immediately.
4. As a maintainer of a private module, I want the check skipped with a reason, so that it never fails spuriously.
5. As a maintainer, I want a short retry when sumdb hasn't caught up yet, so that timing doesn't cause false alarms.

## Implementation Decisions

- Compare against the proxy's zip rather than computing `h1:` locally, and
  treat the result as a post-publish alarm
  ([ADR-0013](../adr/0013-sumdb-check-against-proxy-zip.md)).
- Private detection: `GOPRIVATE`, `GONOSUMDB`, `GONOSUMCHECK`, or the warm
  being disabled.
- Extra files in the archive are listed, not failed.

## Testing Decisions

- Fake sumdb and proxy servers cover match, mismatch, missing (then retry)
  and private.
- A dirhash `h1:` test against a known zip.

## Out of Scope

- A check in `verify`.
- Recording `h1:` in the manifest.
- Verifying the sumdb tree signature.

## Further Notes

- It could become a gate if publishing is reordered: tag push → check →
  attach assets.
