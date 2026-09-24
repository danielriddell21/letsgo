# ADR-0005: `disable` / `require` over a closed feature catalogue

- Status: proposed
- Date: 2026-09-24
- Issue: [#26](https://github.com/danielriddell21/letsgo/issues/26)
- HLD: [hld/features.md](../hld/features.md)

## Context

Features are turned on consistently, through their own directive (`brew`,
`image`, `budget`). Turning one off is inconsistent: some can only be turned
off with a CLI flag that `plan` can't see, and some can't be turned off at
all. A gate can't be made strict. The manifest records gate outcomes, not
the repository's choices.

## Decision

- Two list directives: `disable <feature>...` and `require <feature>...`
  (a `require`d gate turns Skip into Fail).
- They take names from a closed catalogue in `internal/feature`, and
  `TestEveryFeatureIsConsulted` enforces it.
- Integrity features (reproducible build, source archive, manifest,
  checksums, tag check, module path) **cannot** be disabled.
- Features that are off by default are enabled only by their own directive.
- Departures from the defaults are recorded in the manifest's `features`
  field, and `verify` reports them.

## Consequences

- A release's shape can be read from the repository, and a consumer can tell
  an opt-out apart from an older letsgo.
- The per-release override flags (`--allow-*`) stay on the CLI, as decisions
  a person makes about one release.

## Alternatives considered

- **Per-feature directives** (`sbom off`). Rejected: directives multiply.
- **A `features ( on/off )` block.** Rejected: it gives default-off features a
  second way to be enabled.
- **CLI only.** Rejected: `plan` can't see what CI will do.
