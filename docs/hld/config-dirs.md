# HLD: global config and the project `.letsgo/` directory

Status: proposal. Nothing here is implemented.

| | |
|---|---|
| Issue | [#27](https://github.com/danielriddell21/letsgo/issues/27) |
| Epic | [#39](https://github.com/danielriddell21/letsgo/issues/39) |
| PRD | [prd/config-dirs.md](../prd/config-dirs.md) |
| PBS | [pbs/config-dirs.md](../pbs/config-dirs.md) |
| ADRs | [ADR-0006](../adr/0006-global-config-cannot-change-a-release.md), [ADR-0007](../adr/0007-content-addressed-plugin-store.md) |

## Where things are today

| what | where | set by |
| --- | --- | --- |
| the release definition | `letsgo.mod` beside `go.mod` (`plan.ConfigFile`) | repository |
| plugin settings | `letsgo-env.mod` at the root (and `letsgo-cask.mod` in #25) | repository |
| plugin binaries | `$GOBIN` / `$GOPATH/bin`, found on `PATH` (`plugin.resolve`) | `letsgo plugin install` |
| build cache | `os.UserCacheDir()/letsgo/builds` (`build/cache.go`) | fixed |
| git, go | `LETSGO_GIT`, `LETSGO_GO` | env |
| govulncheck, apidiff | GOBIN, GOPATH/bin, `~/go/bin`, then PATH (`gate/tool.go`) | fixed |
| tokens | `--token`, `GITHUB_TOKEN`, `GH_TOKEN`; `LETSGO_TAP_TOKEN` | flag, env |
| plugin source repo | `pluginRepo` constant, `--repo` | flag |
| proxy to warm | `publish.DefaultProxy` constant | fixed |
| forge API | `api.github.com` constant; github.com assumed in `plan.go:850`, `release/install.go:22` | fixed |

The problems:

1. **Plugin pins collide on one machine.** Plugins are looked up on `PATH` by
   name and checked against the pin. Repository A pins `letsgo-multi v0.2.0`,
   repository B pins `v0.3.0`, and only one binary can be in `$GOBIN`. Switching
   repositories means reinstalling, and CI can't cache "the plugins this
   repository pins" as a unit.
2. **Plugin config litters the root.** Each plugin adds a `letsgo-<name>.mod`
   beside `go.mod`. With #25's catalogue and a cask config, that's three
   files, and in a monorepo (#24) it's three per module.
3. **Machine settings exist only as env vars or constants.** A corporate
   module proxy, a plugin mirror, a pinned `go` binary, or a cache on a
   bigger disk has no persistent home, and `plan --explain` can't say where a
   value came from.

## The rule

**Global config may not change what a release is.** Anything that affects the
bytes, the gates, the version or what gets published belongs in `letsgo.mod`,
because `plan` on a laptop, CI, and `verify` on a stranger's machine must reach
the same answer from the same commit. Global config only covers *how this
machine does the work*: where tools, caches and plugin binaries are, which
mirror to fetch through, and how the output looks.

This is enforced with a separate closed set of directives. Writing
`build linux/amd64` or `disable sbom` (#26) in the global file is an error that
says "this belongs in letsgo.mod".

## Project: `.letsgo/`

```
repo/
  go.mod
  letsgo.mod          ← unchanged: the release definition, core directives only
  .letsgo/
    env.mod           ← was letsgo-env.mod
    cask.mod          ← #25
```

- **`letsgo.mod` stays at the root.** It sits beside `go.mod` as the thing a
  reader looks for, and moving it would break every repository for a
  cosmetic gain. The line becomes: `letsgo.mod` is core, `.letsgo/` belongs to
  plugins.
- **Plugins get `.letsgo/<short name>.mod`.** Core passes `config_dir` in every
  hook's input, so a plugin never has to guess where it runs from, and the
  exported SDK (#25) gets `plugin.ConfigFile(in, "env")`. The legacy root name
  is still read, with a plan **Warn** suggesting the move. Finding both is a
  **Fail**, because two configs is one too many.
- **Nothing generated goes in `.letsgo/`.** letsgo keeps no local release
  state (publishing is idempotent against the forge), and `dist/` stays where
  it is. So `.letsgo/` is committed whole, with nothing to gitignore.
- **Monorepo (#24):** each module has its own `.letsgo/` beside its own
  `letsgo.mod`, the same way its `letsgo.mod` is its own.

## Global: `$XDG_CONFIG_HOME/letsgo/config.mod`

Found through `os.UserConfigDir()` (so macOS and Windows get their native
locations), and overridden with `LETSGO_CONFIG`. It uses the same syntax as
`letsgo.mod` and the same parser, with its own directive table.

```
// ~/.config/letsgo/config.mod
go         /usr/local/go1.27.1/bin/go
git        /usr/bin/git
cache      /mnt/big/letsgo-cache
plugins    /mnt/big/letsgo-plugins
plugin-repo acme/letsgo-plugins-mirror
proxy      https://goproxy.acme.internal
color      auto
update-check weekly
```

| directive | replaces | notes |
| --- | --- | --- |
| `go`, `git` | `LETSGO_GO`, `LETSGO_GIT` | same checks as the env vars (absolute path, executable) |
| `tool govulncheck <path>` | GOBIN search | for the gate tools |
| `cache <dir>` | `UserCacheDir/letsgo/builds` | `cache off` too. The cache is never used by `verify`, so this can't affect a verification |
| `plugins <dir>` | — | the plugin store, see below |
| `plugin-repo <owner/name>` | `pluginRepo` constant | a mirror for `plugin install`. The digest pin still decides what runs, so a mirror can't substitute a binary |
| `proxy <url>` | `DefaultProxy` | the proxy that gets warmed. A private module warms the private proxy |
| `color`, `update-check` | — | presentation only |

**Not in global config:**

- **Tokens.** A token in a dotfile is a token in backups and dotfile repos.
  Instead, `token-command gh auth token` tells letsgo to ask a credential
  helper, only after flags and env come up empty. The helper runs on demand,
  and nothing is stored.
- **Forge hosts.** github.com is assumed in core today (`plan.go:850`,
  `release/install.go:22`). GitHub Enterprise support would be a
  repository fact (its remote), not a machine one.

**Precedence:** flag > env > global config > default. Each resolved value is
recorded with `p.note`, so `plan --explain` says `proxy
https://goproxy.acme.internal (from ~/.config/letsgo/config.mod)`.

**Recording.** None of this reaches the manifest, because none of it changes
the release. One exception: the builder section already records the `go`
version, and it keeps recording the version, never the path.

## The plugin store

This fixes problem 1. Plugins become content-addressed:

```
$XDG_DATA_HOME/letsgo/plugins/sha256/<digest>/letsgo-multi
```

- `letsgo plugin install` writes into the store, and also into `$GOBIN` with
  `--link` (for running a plugin by hand).
- `plugin.Run` looks up the pinned digest in the store first, then falls back
  to `PATH` as today. Both paths still re-hash before running, so a store
  entry that has been tampered with is a failure, not a substitution.
- Two repositories pinning two versions coexist, and the same digest pinned by
  ten monorepo modules is stored once.
- `letsgo plugin install` with no arguments installs every pin in `letsgo.mod`.
  That's one command for a fresh clone, and one step in CI.
- CI: cache the store keyed on `hashFiles('**/letsgo.mod')`. letsgo-action can
  do this itself.
- `letsgo plugin list` reports store entries no pin references, and `letsgo
  plugin prune` removes them.

## With the other proposals

- **#24 monorepo:** per-module `.letsgo/`; a shared store removes the cost of
  repeating pins across modules.
- **#25 plugin-aware core:** `config_dir` in hook input; the catalogue's short
  names (`env`, `cask`) are the file names; `plugin-repo` points
  `plugin install` at a mirror.
- **#26 features:** `disable`/`require` are release semantics, so only
  `letsgo.mod` can hold them. The global table rejects them by name, with a
  pointer.

## Alternatives

- **Everything in `.letsgo/`, including `letsgo.mod`.** Tidier, but it hides
  the release definition and breaks every existing repository. Possible later
  as "either location, never both"; not worth it now.
- **A per-project `.letsgo/bin/` instead of a global store.** It solves the
  collisions, but copies the same binary into every clone and needs a
  gitignore. The content-addressed store does the same job once per machine.
- **Global config that can set release defaults** (e.g. an org-wide `build`
  matrix). Rejected by the rule above: a release would depend on who ran it.
  An org default belongs in a template repository, or in a shared workflow
  that runs `letsgo fmt` checks.

## Delivery

Phases, in order. Each is a vertical slice: a thin path through every layer, verified end to end. The behaviour each phase must meet is specified in the [PBS](../pbs/config-dirs.md).

1. Plugin store (user stories 1, 2, 4)
2. `.letsgo/<plugin>.mod` (user stories 5, 6, 12)
3. Global `config.mod` (user stories 7, 8, 9, 10)
4. `token-command`, `color`, `update-check` (user stories 11)
5. letsgo-action caches the store (user stories 3)

## Open questions

- Should `.letsgo/` also accept `letsgo.mod` (either location, never both)?
  Proposed: not now.
- A project-local, gitignored override file (`.letsgo/local.mod`) for machine
  settings per repository? Proposed: no; env vars cover the rare case.
- Should `update-check` exist at all, given `letsgo update --check` and the
  action's `version: latest`? It's the only feature here that makes a network
  call nobody asked for.
