# ADR-0023: Plugins stay separate binaries; they share core's wire contract, not its code

- Status: accepted
- Date: 2026-09-30
- Issue: [#150](https://github.com/danielriddell21/letsgo/issues/150)
- Amends: [ADR-0003](0003-plugin-logic-in-core-thin-mains.md)

## Context

[ADR-0003](0003-plugin-logic-in-core-thin-mains.md) decided that plugin
logic should move into letsgo as importable packages
(`letsgo/plugins/{multi,env,cask}`, one pure function each), with
letsgo-plugins reduced to a one-line `main.go` per plugin calling
`plugin.Main`. `docs/hld/integrated-plugins.md` spells out that shape in
detail and was marked "Status: implemented".

Revisiting this: only part of it happened. `letsgo/plugin` (the wire-type
SDK) and `letsgo/manifest` (the public manifest package) were exported and
are in use. The `letsgo/plugins/{multi,env,cask}` packages — the part of
ADR-0003 that would have moved each plugin's actual logic into core — were
never created. Instead letsgo-plugins kept its own manifest reader, its own
`base()`, and its own config parser, and they have since drifted from core:
cask's `base()` disagrees with `manifest.BaseName` on real archive names,
and its download URL skips `url.PathEscape`, breaking on a monorepo tag
containing `/`.

Given the choice now between finishing the original move (folding each
plugin's logic into core) and instead having each plugin call core's
already-exported building blocks while keeping its own logic, the decision
is the latter.

## Decision

Plugins keep their real logic in their own repo (letsgo-plugins). There is
no `letsgo/plugins/{multi,env,cask}` package in core, and no plan to move
plugin logic into core packages.

Instead:

- letsgo-cask calls `manifest.Read` and `manifest.BaseName` instead of its
  private copies, and builds download URLs through the same path core's
  `github.DownloadURL` uses (escaping included).
- Core adds `plugin.ReadConfig(dir, name)`, honouring `ConfigDir`
  (`plugin/plugin.go:67-142`) and the legacy fallback, for cask and env to
  both call instead of each hand-rolling a `.mod` parser.

This replaces the "one pure function per plugin, folded into core" part of
ADR-0003's decision. Everything else ADR-0003 decided — `letsgo/plugin` as
the public SDK, plugins remaining separate pinned binaries never run by
`verify`, core keeping a catalogue of first-party plugins that only fills in
text — still stands.

## Consequences

- The drift ADR-0003 was written to end (manifest reading, URL building)
  still ends, but by sharing library code rather than by relocating the
  plugin's logic.
- `letsgo/plugins/{multi,env,cask}` is not part of letsgo's public API, so
  it doesn't add to what apidiff gates on every release.
- `docs/hld/integrated-plugins.md`'s "Status: implemented" and its
  `letsgo/plugins/*` proposal are out of date and need correcting to match
  this ADR, not the original ADR-0003 shape.

## Alternatives considered

- **Finish ADR-0003 as written** (move each plugin's logic into
  `letsgo/plugins/*`, reduce letsgo-plugins to one-line mains). Rejected:
  it was tried, didn't happen, and the actual cross-repo friction (manifest
  reading, URL building, config parsing) is solved just as well by plugins
  calling shared exported helpers directly, without needing letsgo-plugins
  to depend on core for its actual behaviour.
