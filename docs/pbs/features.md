# PBS: Feature toggles

> Product-based specification: what the product must do, stated so it can be tested.
> [PRD](../prd/features.md) · [HLD](../hld/features.md) · Issue [#26](https://github.com/danielriddell21/letsgo/issues/26) · ADR [0005](../adr/0005-feature-catalogue-disable-require.md)

## Scope

The `disable` and `require` directives, the feature catalogue, the `letsgo
features` command, and the manifest `features` record.

## Interfaces

| surface | specification |
| --- | --- |
| config | `disable <feature>...` and `require <feature>...`, each repeatable |
| CLI | `letsgo features [--json]` |
| manifest | `features: {disabled: [string], required: [string]}`, omitted when empty |
| verify / plan report | a `features` line listing only departures from the defaults |

### Catalogue

| feature | disable | require |
| --- | --- | --- |
| `reproducible`, `source`, `manifest`, `checksums`, `tag-check`, `module-path` | no | no |
| `vulncheck` | yes | yes |
| `api-gate` | yes | yes |
| `sbom` | yes | no |
| `install-script` | yes | yes |
| `changelog` | yes | no |
| `proxy-warm` | yes | no |
| `brew`, `image`, `budget` | no (remove the directive) | no |

## Requirements

| ID | requirement | story |
| --- | --- | --- |
| FT-1 | `disable X` MUST turn off feature X for the release. | 1 |
| FT-2 | `require X` MUST turn X's Skip into Fail. | 2 |
| FT-3 | `disable` on an integrity feature MUST be a config error saying the feature can't be disabled. | 3 |
| FT-4 | `disable brew`, `disable image` or `disable budget` MUST be an error telling the user to remove the directive. | 4 |
| FT-5 | An unknown feature name MUST be an error with a did-you-mean suggestion. | 5 |
| FT-6 | `letsgo features` MUST list every catalogue feature with its state, where the state came from, and the directive that changes it. | 6 |
| FT-7 | The manifest MUST record disabled and required features, and nothing when all are at their defaults. | 7 |
| FT-8 | `verify` MUST print the `features` line when the field is present. | 8 |
| FT-9 | `--no-proxy-warm` MUST behave as `disable proxy-warm` for one run, and be recorded the same way. | 9 |
| FT-10 | Each module's `letsgo.mod` MUST set features independently. | 10 |
| FT-11 | Every catalogue entry MUST be checked by at least one code path (enforced by a test). | — |
| FT-12 | Plugins MUST NOT be able to change any feature's state. | — |

## Errors and edge cases

- The same feature in both `disable` and `require`: a config error.
- A duplicate name within one directive: a formatting fix, not an error.
- `disable changelog` with `--append-notes`: the existing body is left
  untouched and nothing is appended.

## Acceptance scenarios

1. **Given** `disable sbom`, **when** `release` runs, **then** no SBOM asset exists and the manifest has `features.disabled: ["sbom"]`. (FT-1, FT-7)
2. **Given** `require vulncheck` and no govulncheck on PATH, **when** plan runs, **then** it Fails. (FT-2)
3. **Given** `disable reproducible`, **when** the config is parsed, **then** there's an error at that line. (FT-3)
4. **Given** `disable sbmo`, **when** the config is parsed, **then** the error suggests `sbom`. (FT-5)
5. **Given** a default config, **when** `release` runs, **then** the manifest has no `features` key. (FT-7)
