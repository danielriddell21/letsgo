# ADR-0013: The sumdb check compares the proxy's zip, as a post-publish alarm

- Status: proposed
- Date: 2026-09-24
- Issue: [#32](https://github.com/danielriddell21/letsgo/issues/32)
- HLD: [hld/sumdb.md](../hld/sumdb.md)

## Context

The source archive proves the binaries rebuild, but `go get` users trust
sum.golang.org. The two must describe the same tree. letsgo has zero
dependencies, so `golang.org/x/mod/zip` isn't available.

## Decision

- After the proxy warm, fetch the sumdb `h1:` for `module@version`.
- Download the module zip from the proxy, check its `h1:` against sumdb, then
  compare it **file by file** with the source archive. Every zip file must
  match; extra files in the archive are listed, not failed.
- A mismatch is a loud `✗` and a non-zero exit. The release is already
  public, so this is an alarm, not a gate.
- Private modules (`GOPRIVATE`, `GONOSUMDB`, `GONOSUMCHECK`, or warm disabled)
  Skip the check.

## Consequences

- A mismatch names the differing files.
- There's no hand-rolled module-zip inclusion logic to get subtly wrong.
- v1 trusts sumdb over HTTPS. Tree signatures and inclusion proofs are
  deferred.
- Turning this into a gate later needs a change to the publish order.

## Alternatives considered

- **Compute `h1:` locally** from the archive. Rejected for v1: the zip
  inclusion rules are the risk.
- **A gate before assets are attached.** Deferred.
