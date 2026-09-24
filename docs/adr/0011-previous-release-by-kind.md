# ADR-0011: The previous release is chosen by release kind

- Status: proposed
- Date: 2026-09-24
- Issues: [#29](https://github.com/danielriddell21/letsgo/issues/29), [#33](https://github.com/danielriddell21/letsgo/issues/33)
- HLDs: [hld/promote.md](../hld/promote.md), [hld/what-shipped.md](../hld/what-shipped.md)

## Context

"Previous release" is computed in two ways that disagree:

- `discover.PreviousTag` (git describe) includes prereleases.
- `semver.Latest` (the shallow-clone path) picks the highest version, which is
  wrong for a backport.

The answer feeds the changelog, the API gate, `letsgo tag`, and #33's
"what shipped" section.

## Decision

A single `semver.Previous(tags, current)`, used by every path:

| releasing | previous |
| --- | --- |
| prerelease | the highest release of any kind below it |
| stable | the highest **stable** release below it |
| backport | the highest stable below it |

The git path lists tags with `git tag --merged HEAD` (prefix-filtered per
[ADR-0002](0002-tag-prefix-from-module-dir.md)) and passes them to the same
function.

## Consequences

- The shallow and full-clone paths can't disagree.
- Stable notes and API checks cover everything since the last stable, so a
  break introduced in `rc.1` isn't hidden.
- It fixes the backport bug on its own.

## Alternatives considered

- **Nearest ancestor tag.** Rejected: RCs hide stable-to-stable changes.
- **Highest tag.** Rejected: it breaks backports.
