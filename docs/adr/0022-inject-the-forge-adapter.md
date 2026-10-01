# ADR-0022: Inject the forge adapter instead of building clients inside plan

- Status: accepted
- Date: 2026-09-30
- Issue: [#149](https://github.com/danielriddell21/letsgo/issues/149)

## Context

`internal/plan` resolves credentials (`Token`, `TapToken`, `ReleaseToken`)
and constructs its own `*github.Client` (`checkForge`). Tests fake the forge
by overwriting two package-level variables (`newForgeClient`,
`forgeAPIEndpoint`) rather than passing in a fake. `doRelease`, `runRelease`
and `runPromote` have no end-to-end test as a result — the `--draft` bug
(writing the tap and pushing images despite a draft release) and the missing
sumdb gate on `promote` both got through this gap, and were only caught by
manual review, not by a test.

There are two adapters in practice today, an HTTP client in production and
an in-memory fake in tests, which is what makes this a real seam rather than
a hypothetical one.

## Decision

Resolve credentials once during command wiring, construct the forge client
there, and pass a `Forge` through the interface to plan, release, verify and
promote. Plan stops constructing clients itself.

As built: `plan.Options.NewClient` is a factory (`func(token string)
*github.Client`) that plan calls once the token is resolved, and `cmd/letsgo`
holds a `forge` value, built once in `main`, whose `client` method is that
factory. Every command is a method on it, so a test builds a `forge` pointed
at a fake server instead of overwriting a global. Credential lookups take a
`context.Context`, so a token command is cancelled with the run.

## Consequences

- The two test globals go away.
- Token splitting (release token vs. tap token) has one home.
- Release becomes testable end-to-end through the same adapter tests already
  use for the forge elsewhere in the codebase, closing the gap that let the
  `--draft` and missing-gate bugs through.
- Every caller that currently relies on plan resolving its own client needs
  to be updated to supply one; this is mechanical but touches plan, release,
  verify and promote's call sites.

## Alternatives considered

- **Keep the package-level fakes, just document them better.** Rejected:
  they only fake `plan`'s own calls, not the forge as seen by release,
  verify and promote, which is exactly the coverage gap that let two bugs
  through.
- **Give plan its own `Forge` field but have callers keep constructing
  clients ad hoc elsewhere.** Rejected: that's the status quo's real
  problem — no single place resolves credentials once.
