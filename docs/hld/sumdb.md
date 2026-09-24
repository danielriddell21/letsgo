# HLD: checksum database cross-check

Status: proposal. Nothing here is implemented.

| | |
|---|---|
| Issue | [#32](https://github.com/danielriddell21/letsgo/issues/32) |
| Epic | [#41](https://github.com/danielriddell21/letsgo/issues/41) |
| PRD | [prd/sumdb.md](../prd/sumdb.md) |
| PBS | [pbs/sumdb.md](../pbs/sumdb.md) |
| ADRs | [ADR-0013](../adr/0013-sumdb-check-against-proxy-zip.md) |

## Problem

letsgo ships a source archive and proves the binaries rebuild from it. Most
users, though, get the source through `go get`, which trusts sum.golang.org.
Nothing proves the two are the same tree. A moved tag, a force-push race, or
an archive built from a different tree would go unnoticed.

## Proposal

After `warmProxy` (`cmd/letsgo/main.go:423`), fetch the sumdb record for
`module@version` and compare its `h1:` hash with a hash of the release's
source.

### Steps

1. **Fetch** `https://sum.golang.org/lookup/<escaped module>@<version>`
   (escaped with `publish.EscapeModulePath`), and parse the `h1:` line for
   the zip (not the `/go.mod` line).
2. **Compare** using one of two options:
   - **A. Compute `h1:` locally:** dirhash over the module's files using
     module-zip rules (a `module@version/` prefix, excluding nested modules,
     `vendor/` subtrees and so on, as in `golang.org/x/mod/zip`). This must be
     hand-rolled, since letsgo has zero dependencies, and getting the
     inclusion rules right is the risk.
   - **B. Download the zip from the proxy** and compare it file-by-file with
     the source archive, then compare the zip's `h1:` with sumdb.
     It is simpler, and a mismatch names the differing files.

   **Proposed: B.** It gives a better error, and dirhash over a zip that is
   already in hand is trivial.
3. **Result:**
   - match: `✓ sum.golang.org agrees with the source archive`
   - mismatch: **a loud `✗` and a non-zero exit.** The release is already
     public by now, so this is an alarm, not a gate. The message lists the
     differing files and suggests `letsgo yank`.
   - not in sumdb yet: retry with a short backoff, like the warm. If it's
     still missing, print `!` with the manual command.
4. **Private modules:** if the module path matches `GOPRIVATE`, `GONOSUMDB`
   or `GONOSUMCHECK`, or the proxy warm is disabled (`--no-proxy-warm`, or
   `disable proxy-warm` from #26), the check **Skips** with a reason. It is
   never a Fail.

### What the source archive covers

The letsgo source archive and the module zip may legitimately differ. For
example, the module zip drops nested modules, and the archive might carry
files outside the module. The comparison is over **the module zip's file
set**: every file in the zip must be byte-identical in the archive. Extra
archive files are listed, not failed.

## Out of scope

- A check in `verify` (it could reuse the same function later).
- Recording `h1:` in the manifest.
- Verifying the sumdb tree signature and inclusion proof. v1 trusts HTTPS and
  documents that limit.

## Open questions

- Could this be a **gate**? The proxy needs the tag on the remote, so it could
  run after the tag push and before assets are attached. That needs a
  publish-order change. Proposed: alarm first, gate later.

## Delivery

Phases, in order. Each is a vertical slice: a thin path through every layer, verified end to end. The behaviour each phase must meet is specified in the [PBS](../pbs/sumdb.md).

1. Tracer bullet: fetch and compare (user stories 1, 2, 3)
2. Robustness (user stories 4, 5)
