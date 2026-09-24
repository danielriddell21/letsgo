# ADR-0002: The tag prefix is derived from the module directory

- Status: proposed
- Date: 2026-09-24
- Issue: [#24](https://github.com/danielriddell21/letsgo/issues/24)
- HLD: [hld/monorepo.md](../hld/monorepo.md)

## Context

GoReleaser configures `tag_prefix` and `dir`. Go already defines the answer:
a module in `services/api/` is versioned by `services/api/vX.Y.Z` tags, which
is how `go install …/services/api/cmd/x@v1.2.0` resolves. There are two
monorepo shapes: A, many modules; and B, one module whose commands are
versioned separately.

## Decision

- The prefix is the module directory relative to the git top level, plus
  `/`. There is no config.
- Only shape A is supported. Shape B (`release prefix=api/`) is deferred until
  someone asks for it.
- A release is built with `GOWORK=off`, and a `replace` pointing at a local
  path is a plan **Fail**.

## Consequences

- Scoped releases are ordinary Go module versions, so `go install`, the proxy
  warm and `retract` all work.
- A shape B user must make each product its own module. The docs say so.
- With `GOWORK=off`, a sibling module isn't rebuilt until its `require` is
  bumped, so `letsgo-mono changed` reports that rather than guessing.

## Alternatives considered

- **Configured prefix (the GoReleaser model).** Rejected: a second source of
  truth that can disagree with Go.
- **Shape B first.** Rejected: its versions aren't Go versions, so three of
  letsgo's guarantees would have nothing to attach to.
