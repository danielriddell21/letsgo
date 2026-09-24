# PRD: Global config, `.letsgo/` and the plugin store

| | |
|---|---|
| Issue | [#27](https://github.com/danielriddell21/letsgo/issues/27) |
| Epic | [#39](https://github.com/danielriddell21/letsgo/issues/39) |
| HLD | [hld/config-dirs.md](../hld/config-dirs.md) |
| PBS | [pbs/config-dirs.md](../pbs/config-dirs.md) |
| ADRs | [0006](../adr/0006-global-config-cannot-change-a-release.md), [0007](../adr/0007-content-addressed-plugin-store.md) |

## Problem Statement

Two repositories pinning different versions of the same plugin fight over one
binary in `$GOBIN`. Each plugin drops a `letsgo-<name>.mod` in my repository
root. My machine settings (corporate proxy, pinned go, big-disk cache, plugin
mirror) exist only as env vars or constants, and `plan --explain` can't tell
me where a value came from.

## Solution

A content-addressed plugin store, so pins coexist. A `.letsgo/` directory
holds plugin config. A global `config.mod` holds machine settings only; it
can never change what a release is. Every value records its source.

## User Stories

1. As a developer with two repositories, I want both plugin pins installed at once, so that I don't reinstall when switching.
2. As a developer, I want `letsgo plugin install` with no arguments to install every pin, so that a fresh clone is one command.
3. As a CI author, I want to cache the plugin store keyed on `letsgo.mod`, so that plugin installs are fast.
4. As a developer, I want `letsgo plugin prune` to remove unreferenced store entries, so that the store doesn't grow forever.
5. As a maintainer, I want plugin config in `.letsgo/<plugin>.mod`, so that my repository root stays tidy.
6. As a maintainer with a legacy `letsgo-env.mod`, I want a Warn suggesting the move, and a Fail if both files exist, so that migration is safe.
7. As a developer behind a corporate proxy, I want `proxy` in global config, so that the private proxy is warmed.
8. As a developer, I want `go`, `git`, tool paths and the cache location in global config, so that I stop exporting env vars.
9. As a developer, I want `plan --explain` to say where each machine value came from, so that I can debug precedence.
10. As a maintainer, I want `build` or `disable` in global config to be an error, so that a release never depends on who ran it.
11. As a developer, I want `token-command gh auth token`, so that no token is stored in a file.
12. As a plugin author, I want `config_dir` in hook input, so that I never guess where my config lives.

## Implementation Decisions

- Global config has its own closed table covering machine settings only
  ([ADR-0006](../adr/0006-global-config-cannot-change-a-release.md)).
- The plugin store lives at `$XDG_DATA_HOME/letsgo/plugins/sha256/<digest>/`,
  looked up by the pinned digest and re-hashed before running
  ([ADR-0007](../adr/0007-content-addressed-plugin-store.md)).
- `letsgo.mod` stays at the root. `.letsgo/` is committed whole, with nothing
  generated inside it.
- Precedence is flag > env > global > default. None of it reaches the
  manifest.

## Testing Decisions

- Store: two digests of the same name coexist; a tampered entry fails its
  hash check; `PATH` is the fallback.
- Config: every release directive is rejected in the global table.
- Precedence table tests, with `--explain` attribution.
- Legacy fallback: Warn for one file, Fail for both.

## Out of Scope

- `.letsgo/letsgo.mod` as an alternative location.
- `.letsgo/local.mod`.
- GitHub Enterprise hosts, which are a repository fact.

## Further Notes

- Open question: should `update-check` exist at all? It would be the only
  unrequested network call.
