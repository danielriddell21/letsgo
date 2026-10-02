# ADR-0026: An internal releaser module owns the release flow

- Status: accepted
- Date: 2026-10-02
- Issue: [#230](https://github.com/danielriddell21/letsgo/issues/230)
- Amends: [ADR-0021](0021-apply-module.md)

## Context

`cmd/letsgo`'s `doRelease` runs the whole release in `package main`. It
resolves a Plan, builds, holds the build to a saved plan, splits the forge
clients, renders notes, guards the forge, and then publishes or rehearses.
Nothing tests that sequence end to end. [ADR-0022](0022-inject-the-forge-adapter.md)
records the `--draft` bug that got through as a result.

`letsgo build` and `letsgo plan`'s diff (`planDiff`, which builds into a
scratch directory and reads the forge) open with the same resolve-and-build
steps.

[ADR-0021](0021-apply-module.md) moved plan/apply enforcement into
`internal/apply` and listed `planforge.go` among the files to move. `planDiff`
builds, so putting it in apply would make apply import build and release.
`promote.Run` cannot be copied as `publication.Run` either: CONTEXT.md
defines a Publication as starting from a built Release, and publication
already imports release.

## Decision

- A new `internal/releaser` module sits above plan, release, apply and
  publication. Its interface is three calls:
  - `Build`: resolve and build.
  - `Release`: build, hold, guard, then publish or rehearse.
  - `Diff`: build into a scratch directory and compare against the forge.
- Callers inject the Forge, Tap and Release clients and the resolved token
  through `releaser.Options`, as ADR-0022 says. releaser never reads the
  environment or the machine config.
- releaser writes progress to `Options.Out` and returns a `Result`. `cmd`
  prints the final line.
- `planforge.go` goes to releaser, not apply. `planfile.go` and `planyank.go`
  still go to apply, as ADR-0021 decided. apply stays plan enforcement only.
- `cmd/letsgo`'s `release`, `build`, `plan`, `apply` and `tag` commands only
  map flags and print.

## Consequences

- The release flow has one interface that tests drive with `forgetest` and the
  snapshot recorder, without `t.Chdir` or stdout capture.
- Version proposal moves to `internal/bump`. Tag checkout has one home,
  `discover.CheckoutTag`.
- Publication keeps its glossary meaning: it starts from a built Release.

## Alternatives considered

- **`publication.Run`.** Rejected: the build would become part of
  Publication, which contradicts CONTEXT.md, and publication would have to
  import build.
- **`apply.Release`, and `planDiff` in apply as ADR-0021 listed.** Rejected:
  apply would grow from plan enforcement into the release orchestrator and
  would have to import build and release.
- **Leave it in `cmd/letsgo`.** Rejected: it is the most-changed file and the
  one end-to-end path nothing tests.
