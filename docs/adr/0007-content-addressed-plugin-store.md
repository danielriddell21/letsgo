# ADR-0007: A content-addressed plugin store

- Status: proposed
- Date: 2026-09-24
- Issue: [#27](https://github.com/danielriddell21/letsgo/issues/27)
- HLD: [hld/config-dirs.md](../hld/config-dirs.md)

## Context

Plugins are found on `PATH` by name, then checked against their pin. If two
repositories pin different versions, only one binary can be in `$GOBIN`. CI
also can't cache "this repository's plugins" as a unit.

## Decision

- Store plugins at `$XDG_DATA_HOME/letsgo/plugins/sha256/<digest>/<name>`.
- `plugin.Run` looks up the pinned digest in the store first, then falls back
  to `PATH`. It re-hashes before running either way.
- `plugin install` with no arguments installs every pin, `plugin prune`
  removes unreferenced entries, and `--link` also installs into `$GOBIN`.

## Consequences

- Different versions coexist, and one digest pinned by many modules is stored
  once.
- A tampered store entry fails its hash check; it is never substituted.
- letsgo-action can cache the store keyed on `hashFiles('**/letsgo.mod')`.

## Alternatives considered

- **A per-project `.letsgo/bin/`.** Rejected: it copies binaries into every
  clone and needs a gitignore.
