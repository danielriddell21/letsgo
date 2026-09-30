# PBS: Monorepo releases

> Product-based specification: what the product must do, stated so it can be tested.
> [PRD](../prd/monorepo.md) · [HLD](../hld/monorepo.md) · Issue [#24](https://github.com/danielriddell21/letsgo/issues/24) · ADRs [0001](../adr/0001-monorepo-scope-in-core.md), [0002](../adr/0002-tag-prefix-from-module-dir.md)

## Scope

A Go module nested in a repository is released independently from tags named
`<dir>/vX.Y.Z`, where `<dir>` is its directory relative to the git top level.
This covers shape A only: many modules. `letsgo-mono` covers orchestration
across modules.

## Interfaces

| surface | specification |
| --- | --- |
| working directory | running any `letsgo` command inside a nested module scopes it to that module |
| tag | `<prefix>vX.Y.Z`, where the prefix is `<dir>/` (empty at the root) |
| config | `release latest=auto\|true\|false` (default `auto`) |
| manifest | `tag_prefix` (string, may be empty); `tag` keeps the prefix and `version` is stripped |
| `selfupdate` | `Options.TagPrefix string` |
| `letsgo-mono` | `list [--json]`, `changed [--json]`, `matrix`, `tag [dir...]`, `check` |
| letsgo-action | new output `tag` |

## Requirements

| ID | requirement | story |
| --- | --- | --- |
| MR-1 | The prefix MUST be derived from the module directory; no config may set it. | 1 |
| MR-2 | `plan` MUST accept only tags of the form `<prefix>v<semver>` for a scoped module. | 2 |
| MR-3 | `letsgo tag` MUST propose `<prefix>vX.Y.Z`. | 1 |
| MR-4 | The previous tag MUST be chosen only from tags with the same prefix, using `semver.Previous` (ADR-0011). | 5 |
| MR-5 | The changelog MUST include only commits that touch `<dir>`, excluding nested module directories. | 3 |
| MR-6 | The API gate MUST compare `<dir>` at the previous tag with `<dir>` at HEAD. | 4 |
| MR-7 | Builds MUST run with `GOWORK=off`. | 6 |
| MR-8 | `plan` MUST Fail when `go.mod` has a `replace` pointing at a local path. | 7 |
| MR-9 | Under `latest=auto`, only a root-scope release MAY be marked latest. | 8 |
| MR-10 | `verify` with no tag MUST select the highest release carrying the module's prefix. | 9 |
| MR-11 | `selfupdate` with a `TagPrefix` MUST NOT select a release without that prefix. | 10 |
| MR-12 | The generated `install.sh` MUST download from `releases/download/<tag>/` for scoped releases. | 11 |
| MR-13 | `yank` MUST add the stripped version's `retract` to `<dir>/go.mod`. | 12 |
| MR-14 | `letsgo-mono matrix` MUST output a JSON array of changed releasable module directories, and `[]` when none changed. | 13 |
| MR-15 | `letsgo-mono changed` MUST report a changed dependency whose require hasn't been bumped. It MUST NOT mark the dependent changed. | 14 |
| MR-16 | `letsgo-mono check` MUST report duplicate project names and plugin pins that differ across modules. | 15 |
| MR-17 | The `module <dir>` directive MUST produce a plan Warn and skip the proxy warm. | 16 |
| MR-18 | With an empty scope, every command's output MUST be unchanged from today. | — |

## Errors and edge cases

- A root release with only prefixed tags on HEAD: plan Fails and names the
  module directory to `cd` into.
- A prefixed tag on HEAD that doesn't match the current module: plan Fails
  with "tag belongs to `<other dir>`".
- Tags whose prefix contains `/` MUST be URL-escaped consistently in the
  formula, cask and install.sh.
- A library-only module (no main package, no `letsgo.mod`) appears in `list`
  but not in `matrix`.

## Acceptance scenarios

1. **Given** a repository with a root module tagged `v1.0.0` and `services/api` tagged `services/api/v1.2.0`, **when** `letsgo release` runs in `services/api`, **then** the manifest has `tag: services/api/v1.2.0`, `version: 1.2.0` and `tag_prefix: services/api/`, and `verify services/api/v1.2.0` passes. (MR-2, MR-10)
2. **Given** a root module whose history contains `web/v9.0.0`, **when** the root releases `v1.1.0`, **then** the changelog's previous tag is `v1.0.0`. (MR-4, MR-18)
3. **Given** a root `go.work` that includes a sibling with local edits, **when** `services/api` builds, **then** the digests equal a build without the `go.work`. (MR-7)
4. **Given** a `replace example.com/shared => ../shared`, **when** plan runs, **then** it Fails and cites the `go.mod` line. (MR-8)
5. **Given** `latest=auto`, **when** `services/api` releases, **then** the GitHub release isn't marked latest. (MR-9)
6. **Given** `libs/shared` changed and `services/api` requires its old version, **when** `letsgo-mono changed` runs, **then** it lists `libs/shared`, reports the unbumped require, and doesn't list `services/api`. (MR-15)
