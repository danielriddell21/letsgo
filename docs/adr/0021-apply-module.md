# ADR-0021: Plan/apply enforcement moves into an internal Apply module

- Status: accepted, amended by [ADR-0026](0026-releaser-owns-the-release-flow.md)
- Date: 2026-09-30
- Issue: [#148](https://github.com/danielriddell21/letsgo/issues/148)

## Context

[ADR-0019](0019-plan-apply-enforcement-in-core.md) put plan/apply's
guarantee in core, not a plugin. It didn't say where inside core. Today the
enforcement — the recorder and diff, staleness checks, the "planned actions
only" limit, the guarded forge — is split across four `cmd/letsgo` files
(`planfile.go`, `planforge.go`, `applyguard.go`, `planyank.go`, ~1,000 lines)
next to flag parsing, in `package main`. It can only be tested through
`t.Chdir` plus captured stdout, and `runPlan` routes three different flows
through one function.

The seam already has four real adapters in practice (the HTTP forge, the
snapshot recorder, the read-only observer, the guarded writer used by
`apply`), which is what justifies giving it its own module rather than
leaving it as an internal detail of one command.

## Decision

Move those four files into one `internal/apply` module. Its interface is
diff, save and apply, built on the same `publish.Forge` / `brew.FileAPI`
seams the rest of core already uses. `cmd/letsgo`'s `runPlan` and `runApply`
shrink to mapping flags onto `apply.Options`.

## Consequences

- Tests hit one interface instead of `t.Chdir` plus stdout capture, and can
  run in parallel.
- "What a plan action is" has one home instead of being spread across
  `cmd/letsgo`.
- This is a structural move, not a behaviour change: ADR-0019's and
  ADR-0020's guarantees are unchanged, just relocated.

## Alternatives considered

- **Leave it in `cmd/letsgo`.** Rejected: it's already the deepest logic in
  the binary and the hardest to test; every new plan/apply feature pays the
  `t.Chdir` tax again.
- **Split it into several smaller internal packages** (recorder, guard,
  staleness) instead of one module. Rejected for now: nothing outside plan
  and apply needs them independently, and one module with those as internal
  seams keeps the external interface small.
