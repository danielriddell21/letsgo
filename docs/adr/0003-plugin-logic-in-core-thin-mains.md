# ADR-0003: Plugin logic moves into letsgo; plugins stay as separate pinned binaries

- Status: proposed
- Date: 2026-09-24
- Issue: [#25](https://github.com/danielriddell21/letsgo/issues/25)
- HLD: [hld/integrated-plugins.md](../hld/integrated-plugins.md)

## Context

letsgo-plugins has zero dependencies and redeclares everything it shares with
core: the hook wire types, a manifest subset, and a copy of the tap client.
That duplication has already drifted. URL escaping disagrees with core, and
cask rejects any manifest schema other than 1. Core also deliberately refused
to know plugin names (`describePin`).

## Decision

- letsgo exports `letsgo/plugin` (wire types and `Main`), `letsgo/manifest`,
  and `letsgo/plugins/{multi,env,cask}`, each with one pure function.
- letsgo-plugins keeps only one `main.go` per plugin.
- Core keeps a catalogue of first-party plugins (`plugin.Known`). It fills in
  text and never executes anything.
- Plugins remain separate binaries, pinned by digest, and never run by
  `verify`.

## Consequences

- The drift ends: one set of types, one manifest reader, one URL builder.
- The new public API falls under letsgo's own apidiff gate. Each plugin
  package is kept to one function over the wire types.
- Pins churn more, because a plugin's digest now changes whenever the letsgo
  code it links changes.
- letsgo-plugins gains a `require` on letsgo.

## Alternatives considered

- **Status quo** (zero-dependency plugins). Rejected: it keeps paying for the
  drift.
- **Built-ins** (`archive bundle`, `brew cask` directives). Rejected for now:
  it gives up opt-in pinning, and letsgo-env is exactly what core refuses to
  do. Revisit for multi if most repositories pin it.
