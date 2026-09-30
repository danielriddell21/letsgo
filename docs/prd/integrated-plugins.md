# PRD: Plugin-aware core, thin plugin mains

| | |
|---|---|
| Issue | [#25](https://github.com/danielriddell21/letsgo/issues/25) |
| Epic | [#39](https://github.com/danielriddell21/letsgo/issues/39) |
| HLD | [hld/integrated-plugins.md](../hld/integrated-plugins.md) |
| PBS | [pbs/integrated-plugins.md](../pbs/integrated-plugins.md) |
| ADRs | [0003](../adr/0003-plugin-logic-in-core-thin-mains.md), [0004](../adr/0004-core-writes-tap-files.md) |

## Problem Statement

letsgo-plugins duplicates core: the hook types, a manifest reader, and a tap
client. The copies have drifted. letsgo-cask builds unescaped URLs, breaks
on any new manifest schema, asks me to type facts core already knows, and
writes the tap outside the release, with no snapshot and no rollback on
yank. Core also can't tell me which first-party plugins exist, or print a
complete pin.

## Solution

letsgo exports the plugin SDK, the manifest reader, and each plugin's logic.
letsgo-plugins shrinks to one `main.go` per plugin. Core knows the
first-party plugins by name and prints complete pins. A new `tap-files` hook
lets letsgo-cask render casks while core writes them, with the formula's
guarantees.

## User Stories

1. As a maintainer, I want `letsgo plugin install letsgo-cask` to print a complete pin, so that I can paste it without looking up the hook.
2. As a maintainer, I want `letsgo plugin list --available` to show the first-party plugins, so that I can discover them.
3. As a maintainer, I want plan to suggest letsgo-cask when I have a darwin variant and a brew tap, so that I find the right tool.
4. As a maintainer, I want pinning a known plugin to the wrong hook to fail plan with the right hook named, so that mistakes are obvious.
5. As a cask user, I want the cask written during the release, so that it can't lag behind the formula.
6. As a cask user, I want `--snapshot` to show the cask that would be written, so that I can rehearse it.
7. As a cask user, I want `letsgo yank` to roll the cask back, so that it doesn't point at a retracted release.
8. As a cask user, I want the description, licence and homepage taken from the repository, so that I don't pass flags.
9. As a security-minded maintainer, I want the plugin never to see a tap token, so that a plugin can't leak it.
10. As a plugin author, I want to import `letsgo/plugin` for the wire types and `Main`, so that my types never drift from core's.
11. As a plugin author, I want `letsgo/manifest` to decode newer schemas tolerantly, so that a schema bump doesn't break me.
12. As a third-party plugin author, I want my plugin to keep working without being in the catalogue, so that the ecosystem stays open.

## Implementation Decisions

- The logic moves into letsgo, and the plugins stay separate pinned binaries
  ([ADR-0003](../adr/0003-plugin-logic-in-core-thin-mains.md)).
- The `tap-files` hook renders only; core validates paths, writes, rehearses
  and yanks ([ADR-0004](../adr/0004-core-writes-tap-files.md)).
- The public API added is `letsgo/plugin`, `letsgo/manifest` and
  `letsgo/plugins/{multi,env,cask}`, one function each.
- The catalogue `plugin.Known` maps name → hook and summary. It never
  executes anything.
- Cask settings (`variant`, `token`) move to `letsgo-cask.mod`, or
  `.letsgo/cask.mod` per #27.
- The manifest gains `tap_files: [{path, sha256}]`.

## Testing Decisions

- The JSON-to-JSON plugin tests move with the logic into letsgo.
- letsgo-plugins keeps one smoke test per binary: sample stdin, with the hook
  name enforced.
- `tap-files` path validation: absolute paths, `..`, paths outside `Casks/`,
  and clashes with formulas.
- A yank test: the cask is re-rendered from the previous manifest.
- A slashed-tag URL test, shared with #24.

## Out of Scope

- `tap-files` writing under `Formula/`.
- Folding letsgo-plugins into the letsgo repository.
- Built-in directives replacing plugins.

## Further Notes

- The cost is pin churn: plugin digests change whenever the letsgo code they
  link changes.
