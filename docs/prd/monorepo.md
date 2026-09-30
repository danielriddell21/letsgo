# PRD: Monorepo releases

| | |
|---|---|
| Issue | [#24](https://github.com/danielriddell21/letsgo/issues/24) |
| Epic | [#38](https://github.com/danielriddell21/letsgo/issues/38) |
| HLD | [hld/monorepo.md](../hld/monorepo.md) |
| PBS | [pbs/monorepo.md](../pbs/monorepo.md) |
| ADRs | [0001](../adr/0001-monorepo-scope-in-core.md), [0002](../adr/0002-tag-prefix-from-module-dir.md) |

## Problem Statement

I keep several Go modules in one repository (`services/api`,
`services/worker`, `libs/shared`). Go versions each one with prefixed tags
(`services/api/v1.2.0`), but letsgo only understands root tags. I can't
release one module independently. Worse, once a prefixed tag exists, a root
release can pick it as its "previous" tag, and a root `go.work` silently
builds sibling modules from disk instead of their required versions.

## Solution

Running `letsgo` inside a nested module releases that module from its
prefixed tag, with no config: the prefix is the module directory. The
changelog, API gate, tag proposal, yank, verify, selfupdate and install.sh
all respect the prefix. A companion, `letsgo-mono`, lists the modules, says
which changed, emits a CI matrix, and tags modules in dependency order.

## User Stories

1. As a maintainer, I want `cd services/api && letsgo tag` to propose `services/api/v1.2.0`, so that I don't have to type prefixes.
2. As a maintainer, I want `letsgo release` in a nested module to release only that module, so that modules ship independently.
3. As a maintainer, I want the changelog to list only commits touching the module's directory, so that the notes are relevant.
4. As a maintainer, I want the API gate to compare the module against its own previous tag, so that breaks are caught per module.
5. As a maintainer, I want a root release never to pick a prefixed tag as its previous one, so that root changelogs are correct today.
6. As a maintainer, I want releases to build with `GOWORK=off`, so that what ships is what `go install mod@v` builds.
7. As a maintainer, I want a plan failure when a module `replace`s a local path, so that I don't ship something `go install` refuses.
8. As a maintainer, I want only the root module to take GitHub's "latest" by default, so that unscoped consumers aren't broken.
9. As a user, I want `verify` with no tag to pick the highest release with the module's prefix, so that I verify the right thing.
10. As a library author, I want `selfupdate` to accept a `TagPrefix`, so that my binary never updates into a sibling module's releases.
11. As a user, I want the generated `install.sh` to download from the scoped release, so that install works for nested modules.
12. As a maintainer, I want `yank` to retract the stripped version in the module's own `go.mod`, so that `go get` stops offering it.
13. As a CI author, I want `letsgo-mono matrix` to output the modules that changed, so that one workflow releases only those.
14. As a maintainer, I want `letsgo-mono changed` to tell me when a dependency module changed but its require hasn't been bumped, so that I release in the right order.
15. As a maintainer, I want `letsgo-mono check` to flag duplicate project names and drifted plugin pins, so that cross-module problems are visible.
16. As a user of the `module <dir>` directive, I want a warning that the root tag isn't a version of that module, so that I understand why the proxy warm fails.

## Implementation Decisions

- The scope `(Dir, Prefix)` is resolved in core
  ([ADR-0001](../adr/0001-monorepo-scope-in-core.md)).
- The prefix is derived from the module directory, only shape A (many
  modules) is supported, and builds use `GOWORK=off`
  ([ADR-0002](../adr/0002-tag-prefix-from-module-dir.md)).
- The manifest gains `tag_prefix` (an additive schema bump).
- A new `release latest=auto|true|false` setting; `auto` means true for the
  root scope only.
- Existing plugins (multi, env, cask) are unchanged. Each module is one
  ordinary release.
- There is no config inheritance between modules.

## Testing Decisions

- The fixture repository has a root module and `services/api`, both tagged,
  released and verified end to end.
- Table tests cover each changed function with an empty scope (proving no
  behaviour change) and with a prefixed scope.
- Regression tests cover a root release whose history contains a `web/v…`
  tag, a root `go.work`, and a local `replace`.
- The formula writer and letsgo-cask each get a test with a slashed tag.

## Out of Scope

- Shape B (one module, separately versioned commands). Deferred.
- Automatically bumping requires between modules.
- Extra hook inputs (`tag`, `module_dir`) until a plugin asks for them.

## Further Notes

- Open questions: should a release at the root with only prefixed tags on HEAD
  refuse, or name the module to `cd` into? Should the release title use the
  full directory or the project name?
