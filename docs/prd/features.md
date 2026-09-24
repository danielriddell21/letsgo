# PRD: Feature toggles (`disable` / `require`)

| | |
|---|---|
| Issue | [#26](https://github.com/danielriddell21/letsgo/issues/26) |
| Epic | [#39](https://github.com/danielriddell21/letsgo/issues/39) |
| HLD | [hld/features.md](../hld/features.md) |
| PBS | [pbs/features.md](../pbs/features.md) |
| ADRs | [0005](../adr/0005-feature-catalogue-disable-require.md) |

## Problem Statement

I can turn letsgo features on with a directive, but turning them off happens
through CLI flags hidden in workflows, or isn't possible at all (SBOM,
install.sh). `plan` on my laptop can't see what CI will do. I can't make the
vulnerability gate strict: it silently Skips when govulncheck is missing.
Consumers can't tell "no SBOM by choice" from "published by an older
letsgo".

## Solution

Two directives in `letsgo.mod`, `disable` and `require`, over a closed
catalogue of features. Integrity features can't be disabled. Departures from
the defaults are recorded in the manifest and shown by `verify`. A new
`letsgo features` command lists every feature, its state, and how to change
it.

## User Stories

1. As a maintainer, I want to write `disable sbom` in `letsgo.mod`, so that the choice lives with the release definition.
2. As a maintainer, I want `require vulncheck` to turn a missing govulncheck into a Fail, so that the gate can't silently skip.
3. As a maintainer, I want `disable reproducible` to be an error, so that integrity can't be switched off.
4. As a maintainer, I want `disable image` to tell me to remove the `image` directive, so that there's one way to control each feature.
5. As a maintainer, I want did-you-mean on misspelled feature names, so that typos are caught.
6. As a maintainer, I want `letsgo features` to show each feature's state and where it came from, so that I understand my release.
7. As a consumer, I want the manifest to record disabled and required features, so that I can tell an opt-out from an older letsgo.
8. As a consumer, I want `verify` to report features, so that I see opt-outs where I see gates.
9. As a maintainer, I want `--no-proxy-warm` to keep working as a one-run `disable proxy-warm`, so that existing workflows don't break.
10. As a monorepo maintainer, I want each module's `letsgo.mod` to set its own features, so that a library module can disable install.sh.

## Implementation Decisions

- A closed catalogue in `internal/feature`, driving validation, plan,
  report, manifest and the command
  ([ADR-0005](../adr/0005-feature-catalogue-disable-require.md)).
- Disable is allowed for vulncheck, api-gate, sbom, install-script, changelog
  and proxy-warm. Require is allowed for vulncheck, api-gate and
  install-script.
- The manifest gains `features: {disabled, required}`, recording only
  departures from the defaults.
- `--allow-*` stay on the CLI as decisions about one release. No generic
  `--disable` flag is added.
- Plugins can't disable core features.

## Testing Decisions

- `TestEveryFeatureIsConsulted`: every catalogue entry is checked by some code
  path.
- Config tests for disallowed names, directive-owned features, and
  did-you-mean.
- Manifest round-trip, including an empty `features` field for default
  releases.
- A require test: a missing tool becomes Fail.

## Out of Scope

- `require` for outputs beyond install-script.
- Auto-skipping api-gate for modules with no importable packages (an open
  question).

## Further Notes

- `disable changelog` leaves an existing body untouched (proposed).
