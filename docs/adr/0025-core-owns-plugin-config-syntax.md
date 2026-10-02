# ADR-0025: Core owns Plugin config syntax, not just its location

- Status: accepted
- Date: 2026-10-02
- Issue: [#226](https://github.com/danielriddell21/letsgo/issues/226)
- Amends: [ADR-0023](0023-plugins-share-cores-wire-contract.md)

## Context

[ADR-0023](0023-plugins-share-cores-wire-contract.md) added
`plugin.ReadConfig(dir, name)`, so a Plugin no longer guesses where its
Plugin config lives. It returns only bytes. letsgo-env and letsgo-cask then
each re-implement the same line grammar: scanning, `//` comments, line
numbers, and `path:line:` errors. That grammar is a weaker copy of the one
`internal/config` already parses for letsgo.mod.

`ReadConfig` also looks for the legacy `letsgo-<name>.mod` relative to the
working directory, so every Plugin test that touches config calls
`t.Chdir`.

## Decision

- Move letsgo.mod's syntax layer (lines, blocks, comments, positions; no
  meaning) out of `internal/config` into a new public package,
  `letsgo/modsyntax`. `internal/config` imports it. letsgo.mod and every
  Plugin config share one grammar.
- Add a Plugin config reader to `letsgo/plugin`. It returns flattened lines,
  where a line inside a block carries the block's keyword, plus the path the
  lines came from. A Plugin never walks blocks itself.
- The reader finds the repository root as the parent of `ConfigDir`, which
  core always sets to `<root>/.letsgo`. The legacy `<root>/letsgo-<name>.mod`
  fallback stays. An empty `ConfigDir` is an error, not a lookup in the
  working directory.
- The reader assigns no meaning to keywords. Each Plugin keeps its own
  vocabulary and its own duplicate rule: env rejects a repeated symbol, cask
  a repeated directive.
- `plugin.ReadConfig` is marked `Deprecated:` and points at the new reader.
  It is removed at the next breaking release.

## Consequences

- One grammar, tested once in core. Plugin config errors read like
  letsgo.mod's, as `path:line:col`.
- A Plugin config gains blocks and quoted arguments. Both are syntax, not
  logic, so letsgo-env's rule that its config cannot carry logic still holds.
- Plugin tests use temp directories and drop `t.Chdir`.
- `letsgo/modsyntax` and the new reader fall under letsgo's apidiff gate.
  `internal/config`'s syntax changes are now public API changes.

## Alternatives considered

- **A new minimal grammar in `plugin`** (one directive per line, `//`
  comments). Rejected: this would be a second parser beside letsgo.mod's,
  and the two would drift like the copies ADR-0023 removed.
- **Exporting the syntax layer only inside `plugin`.** Rejected: letsgo.mod
  would keep its own copy, so there would still be two parsers.
- **Core enforcing duplicate keys.** Rejected: env and cask treat different
  things as the key, so this would need a per-directive rule in the
  interface.
- **A `repo_root` field on every hook input.** Rejected: it changes the wire
  contract of three hooks, to carry a value `ConfigDir` already implies.
- **Removing `ReadConfig` now.** Rejected: it breaks the public API without
  a breaking release.
