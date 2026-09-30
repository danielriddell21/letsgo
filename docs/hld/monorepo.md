# HLD: monorepo releases

Status: implemented. Orchestration across modules (which changed, the CI matrix) is left to CI; `letsgo-action` exposes a `tag` output for it.

| | |
|---|---|
| Issue | [#24](https://github.com/danielriddell21/letsgo/issues/24) |
| Epic | [#38](https://github.com/danielriddell21/letsgo/issues/38) |
| PRD | [prd/monorepo.md](../prd/monorepo.md) |
| PBS | [pbs/monorepo.md](../pbs/monorepo.md) |
| ADRs | [ADR-0001](../adr/0001-monorepo-scope-in-core.md), [ADR-0002](../adr/0002-tag-prefix-from-module-dir.md) |

GoReleaser's `monorepo` block releases one subdirectory of a repository from a
prefixed tag:

```yaml
monorepo:
  tag_prefix: services/api/
  dir: services/api
```

`services/api/v1.2.0` becomes version `1.2.0`, the previous tag is the previous
`services/api/` tag, the changelog only lists commits touching `services/api`,
and the build runs in that directory. This document works out what the letsgo
equivalent is, what has to be core, and how it composes with
`letsgo-multi`, `letsgo-env` and `letsgo-cask`.

## Summary

- **Go already defines this.** A module in `services/api/` is versioned by
  tags named `services/api/vX.Y.Z` — that is how `go install
  example.com/repo/services/api/cmd/x@v1.2.0` resolves. letsgo derives
  everything else from `go.mod` and git, so the tag prefix should be derived
  too: the module directory relative to the repository root. No config.
- **It cannot be a hook plugin.** Which tag is being released is decided before
  anything a hook sees, and `verify`, `yank` and `selfupdate` need it without
  any plugin installed. It touches about a dozen places in core (listed below).
- **Orchestration is CI's job.** Which modules changed and what to release
  in what order is a question about the repository, not about a release, and
  a CI path filter answers it without letsgo in the loop.
- **Existing plugins need no changes.** Each module is its own release with its
  own `letsgo.mod`, so `letsgo-multi`, `letsgo-env` and `letsgo-cask` each see
  one ordinary release. The friction is repetition (one pin per module).

## Two shapes of monorepo

| | A. many modules | B. one module, many products |
| --- | --- | --- |
| layout | `services/api/go.mod`, `services/worker/go.mod`, `go.work` at the root | one `go.mod`, `cmd/api`, `cmd/worker` versioned separately |
| tag | `services/api/v1.2.0` | `api/v1.2.0` |
| is the tag a Go module version? | yes | no |
| `go install …@v1.2.0` | works | does not |
| proxy warm, `retract` | apply | do not apply |

Shape A is Go-native and is what this design supports. Shape B is how
GoReleaser users often arrive, but its version is not one Go knows about, so
three of letsgo's guarantees (`go install`, the proxy, `yank`'s retract) have
nothing to attach to. It is sketched as a later phase at the end, and the
honest first answer to it is "make each product a module".

## Why not a hook plugin

The plugin contract (`letsgo/internal/plugin/plugin.go`) is: a closed set of
hooks, each answering a question core already asks, and every answer recorded
in the manifest so that `verify` never runs a plugin.

A `scope` hook answering "which tag is this release, and which directory" fails
that test three ways:

1. **Verify cannot replay it.** `verify` finds the manifest *by tag*
   (`internal/verify/verify.go:163`). The tag cannot be a recorded answer that
   is only readable once you already know the tag.
2. **Consumers outside a release need it.** `selfupdate.Check` and the
   generated `install.sh` ask the forge for `releases/latest`, which in a
   monorepo is whichever module released last. A program importing
   `selfupdate` will not have a plugin installed.
3. **It is not a question, it is a fact.** A hook exists where a repository
   might legitimately answer differently. For shape A Go has already answered.

So the prefix is core.

## Core changes

Everything hangs off one new value: the **scope**, `(Dir, Prefix)`, where
`Dir` is the module directory relative to the git top level and `Prefix` is
`Dir + "/"`, or empty for a module at the root. Every repository today has an
empty scope, and every change below is a no-op for it.

Running a scoped release is just running letsgo inside the module:

```sh
cd services/api
git tag services/api/v1.2.0     # or: letsgo tag
letsgo plan
letsgo release
```

| # | where | today | change |
| --- | --- | --- | --- |
| 1 | `discover.FindGit` | no top level | also return `git rev-parse --show-toplevel`; `Scope` is `FindModule(dir).Dir` relative to it |
| 2 | `plan.resolveVersion` (`plan.go:1182`) | a version tag is `v[0-9]…` | a version tag is `Prefix + "v[0-9]…"`; `Tag` keeps the prefix, `Version` and `CheckTag` use the stripped `vX.Y.Z` |
| 3 | `discover.PreviousTag` | `git describe --tags` over every tag | `--match '<prefix>v[0-9]*'`. **This is a bug fix for root releases too**: once any `web/v…` tag exists, a root release's previous tag can be it |
| 4 | `discover.Commits` | whole history | `git log … -- <Dir>` excluding nested module dirs (same rule as a Go module zip) |
| 5 | `changelog.Collect`, shallow path | `semver.Latest(tags, tag)` over all tags | filter by prefix; forge commits by `?path=<Dir>` rather than compare |
| 6 | API gate `checkoutTag` (`plan.go:715`) | compares the worktree root | compares `<worktree>/<Dir>` |
| 7 | `letsgo tag` (`main.go:603`) | proposes `vX.Y.Z` | proposes `<prefix>vX.Y.Z`, bump computed from scoped commits |
| 8 | `yank` | retracts in `./go.mod` | retracts the stripped `vX.Y.Z` in `<Dir>/go.mod`; finds the release by full tag |
| 9 | `verify [tag]`, no tag | `releases/latest` | highest-semver release whose tag has the prefix |
| 10 | `selfupdate.Options` | `releases/latest` | new `TagPrefix`; list releases and pick the highest with that prefix. The prefix is compiled in, so the updater cannot wander into a sibling module's releases |
| 11 | generated `install.sh` | `releases/latest/download` | for a scoped release, `releases/download/<tag>/`; the script already carries the digests, so it was pinned in effect anyway |
| 12 | forge release | `make_latest` implicit | `release latest=auto\|true\|false`; `auto` is true for the root scope only. GitHub has one "latest" per repository, and a module release quietly taking it breaks every unscoped consumer |
| 13 | manifest | `module_dir` | add `tag_prefix` (schema bump, additive) so a reader need not re-derive it |
| 14 | `gobuild` env | `GOWORK` unset, so a root `go.work` is picked up | `GOWORK=off`. A release must build what `go install mod@v` builds; a workspace substitutes sibling modules from disk |
| 15 | plan check, new | — | **Fail** on a `replace` to a local path: `go install @version` refuses it, and the source archive (the module's tracked files) cannot rebuild it |

### The existing `module <dir>` directive

`module web` today releases `./web` under a *root* tag (`v1.2.0`), as "one
project with one version and one tag" (`plan.go:1122`). That tag is not a
version of the module `…/web`, so:

- `publish.WarmProxy(p.Module.Path, p.Version)` (`main.go:424`) asks the proxy
  for `…/web@v1.2.0`, which does not exist — it fails every time, as a
  warning;
- `go install …/web/cmd/x@v1.2.0` does not resolve.

Keep the directive (it is the right tool when `web` is split out for
dependency hygiene but versioned with the repository), but add a plan **Warn**
saying so, and skip the proxy warm for it. Scoped releases are the answer for
anything that wants its own version.

## CI

Orchestration stays in the workflow. A tag-triggered workflow needs no matrix
at all:

```yaml
on:
  push:
    tags: ['**/v*']

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
        with: { fetch-depth: 0 }
      - id: module
        run: echo "dir=${GITHUB_REF_NAME%/v*}" >> "$GITHUB_OUTPUT"
      - uses: actions/setup-go@v7
        with: { go-version-file: '${{ steps.module.outputs.dir }}/go.mod' }
      - uses: danielriddell21/letsgo-action@v1
        with:
          working-directory: ${{ steps.module.outputs.dir }}
```

The module is the tag with `/vX.Y.Z` cut off. For a workflow that decides what
to release from the diff, a path filter or a static matrix of module
directories does the same job. `letsgo-action` runs a scoped release with no
change (its manifest search is relative to `working-directory`) and exposes the
release's `tag` as an output, because `version` alone no longer identifies the
release.

A dependency module changing does not make its dependents change: under
`GOWORK=off`, `services/api` builds the `libs/shared` version its `go.mod`
requires, so its bytes are unchanged until someone bumps that require. That is
a commit with consequences, and it belongs to a person or to Dependabot.

## With the existing plugins

Each module has its own `letsgo.mod`, loaded from the module directory, and
plugins run with that directory as their working directory
(`plugin.Run(…, p.RootDir, …)`, where `RootDir` is the module when letsgo runs
inside it). So every plugin sees exactly one ordinary release.

### `letsgo-multi`

Unchanged. Its input is the module's project and commands, so a
`tools/` module with five commands ships as one `tools_1.2.0_<os>_<arch>`
archive, and `services/api` next to it keeps one archive per command unless it
pins multi too. Multi is per module, which is right: whether a module's
commands are one product is a question about that module.

The cost is that five modules wanting multi pin it five times. Config
inheritance (a root `letsgo.mod` that nested ones extend) is rejected: it
makes a root edit silently change every module's release, which is the kind of
action at a distance the manifest exists to rule out.

### `letsgo-env`

Unchanged. It receives `Version` (stripped, `1.2.0`) and `Module` (the nested
path), which is what it compiles in. Its own config file is read from the
working directory, so each module gets its own. Two modules released in one job
share one environment; the matrix above gives each its own job.

### `letsgo-cask`

Needs one check, no design change. It reads `tag` from `letsgo.json` and
builds download URLs from it; a tag containing `/` has to reach GitHub as
`releases/download/services/api/v1.2.0/…`. GitHub serves that, but the cask and
the formula writer should each get a test with a slashed tag rather than
relying on it.

### Additive hook inputs

`ArchiveLayoutInput` and `LDFlagsInput` could carry `tag` and `module_dir`.
Nothing needs them yet, and `hook.Main` decodes without
`DisallowUnknownFields`, so adding them later is backwards compatible. Leave
them out until a plugin asks.

## Shape B, later

If it is wanted: a `letsgo.mod` may live below its `go.mod`, found by walking
up from the working directory to the module root, and `release prefix=api/`
names the tag prefix. Commands are limited to those under the config's
directory. The plan says, as a Warn, that the version is not a Go module
version, and skips the proxy warm, `retract`, and the API gate. This is the
part closest to GoReleaser's model and furthest from Go's, which is why it
comes last.

## Delivery

Phases, in order. Each is a vertical slice: a thin path through every layer, verified end to end. The behaviour each phase must meet is specified in the [PBS](../pbs/monorepo.md).

1. Root-release bug fixes (user stories 5, 6, 7, 13)
2. Tracer bullet: a scoped plan and build (user stories 1, 2)
3. Scoped history (user stories 3, 4)
4. Scoped publish and consumers (user stories 8, 9, 10, 11, 12)

## Decided

- Should `letsgo release` run at the repository root refuse when HEAD carries
  only prefixed tags, or name the module to `cd` into? Neither: `Scope{}`'s
  `MatchesTag` already rejects a prefixed tag for a root scope (it doesn't
  start with `v`), so an unreleased root module in an otherwise-tagged
  monorepo reads as "no previous release," same as any first release. That
  is usually the true state of the world — a root module can be added to an
  existing monorepo — so no refusal or redirect was added.
- Release title: `services/api v1.2.0`, or the project name (`api v1.2.0`)?
  Settled on the module's directory plus the version — `services/api
  v1.2.0` — over the bare project name, since two modules with the same base
  name (`services/api`, `tools/api`) would otherwise read identically on the
  releases page. A root module's title is unchanged: the bare tag.
  Implemented as `releaseTitle` in `cmd/letsgo/main.go` and mirrored in
  `internal/promote/promote.go`.
