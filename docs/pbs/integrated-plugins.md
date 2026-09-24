# PBS: Plugin-aware core, thin plugin mains

> Product-based specification: what the product must do, stated so it can be tested.
> [PRD](../prd/integrated-plugins.md) · [HLD](../hld/integrated-plugins.md) · Issue [#25](https://github.com/danielriddell21/letsgo/issues/25) · ADRs [0003](../adr/0003-plugin-logic-in-core-thin-mains.md), [0004](../adr/0004-core-writes-tap-files.md)

## Scope

The public SDK and manifest packages, a first-party plugin catalogue, and a
`tap-files` hook whose output core writes to the Homebrew tap.

## Interfaces

| surface | specification |
| --- | --- |
| Go packages | `letsgo/plugin` (wire types, `Main[In, Out](hook, fn)`), `letsgo/manifest` (`Read`), `letsgo/plugins/{multi,env,cask}` (one exported func each) |
| CLI | `letsgo plugin install <name>` prints a complete pin; `letsgo plugin list --available` |
| config | `plugin tap-files <cmd> <version> sha256:<digest>`; cask settings in `letsgo-cask.mod` (`variant`, `token`) |
| hook `tap-files` input | `{project, version, tag, repo, tap, description, license, homepage, caveats, artifacts: [{archive, variant, os, arch, sha256, url, binaries}]}` |
| hook `tap-files` output | `{files: [{path, content}]}` |
| manifest | `tap_files: [{path, sha256}]` |

## Requirements

| ID | requirement | story |
| --- | --- | --- |
| IP-1 | `plugin install <known name>` MUST print `plugin <hook> <name> <version> sha256:<digest>`. | 1 |
| IP-2 | `plugin list --available` MUST list every catalogue entry with its hook and summary. | 2 |
| IP-3 | `plan` SHOULD suggest letsgo-cask when a darwin variant and a `brew` tap are both configured. | 3 |
| IP-4 | `plan` MUST Fail when a known plugin is pinned to a hook other than its catalogue hook, and name the correct one. | 4 |
| IP-5 | Unknown (third-party) plugins MUST install, pin and run as they do today. | 12 |
| IP-6 | Every `tap-files` output path MUST be relative, contain no `..`, be under `Casks/`, and not collide with a formula path; otherwise the release Fails before publishing. | 5 |
| IP-7 | Core MUST write the tap files in the same tap update as the formula, with the same author. | 5 |
| IP-8 | `--snapshot` MUST record the tap files without writing them. | 6 |
| IP-9 | Drafts MUST skip the tap files, with the same message as the formula. | — |
| IP-10 | `yank` MUST re-run the tap-files plugin against the previous release's manifest and write the result. | 7 |
| IP-11 | The description, licence and homepage MUST come from the repository, not flags. | 8 |
| IP-12 | The plugin process environment MUST NOT contain any forge or tap token. | 9 |
| IP-13 | `letsgo/manifest.Read` MUST decode manifests with a higher schema, ignoring unknown fields. | 11 |
| IP-14 | Exported packages MUST pass letsgo's apidiff gate on every release. | 10 |

## Errors and edge cases

- The plugin exits non-zero or prints invalid JSON: the release Fails, and
  stderr is shown.
- The plugin returns zero files: no tap write, and no error.
- Two plugins return the same path: Fail.
- A tag containing `/` (#24): URLs come from core's `DownloadURL`, never from
  the plugin.

## Acceptance scenarios

1. **Given** `plugin tap-files letsgo-cask …` and a gui variant, **when** `release` runs, **then** `Casks/<token>.rb` is committed alongside the formula and `tap_files` lists its sha256. (IP-7)
2. **Given** a fixture plugin that returns `../Formula/x.rb`, **when** `release` runs, **then** it Fails before any asset is uploaded. (IP-6)
3. **Given** a released cask, **when** `letsgo yank v1.3.0` runs, **then** the cask matches the one rendered for `v1.2.0`. (IP-10)
4. **Given** `plugin ldflags letsgo-multi …`, **when** plan runs, **then** it Fails with "letsgo-multi answers archive-layout". (IP-4)
5. **Given** a schema-2 manifest, **when** letsgo-cask reads it, **then** no error is raised. (IP-13)
