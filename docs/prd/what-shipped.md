# PRD: "What shipped" in release notes

| | |
|---|---|
| Issue | [#33](https://github.com/danielriddell21/letsgo/issues/33) |
| Epic | [#41](https://github.com/danielriddell21/letsgo/issues/41) |
| HLD | [hld/what-shipped.md](../hld/what-shipped.md) |
| PBS | [pbs/what-shipped.md](../pbs/what-shipped.md) |
| ADRs | [0011](../adr/0011-previous-release-by-kind.md) |

## Problem Statement

My release notes say what the commits *claim* changed. What actually shipped
(a toolchain bump, a new dependency, a 20% size jump) is available from
`letsgo diff`, but nobody runs that by hand, and it can't produce markdown
or JSON.

## Solution

Release notes gain a collapsed "What shipped" section built from the manifest
diff against the previous release. `letsgo diff` gains `--format md|json`.

## User Stories

1. As a user, I want to see toolchain, dependency and size changes in the release notes, so that I know what I'm installing.
2. As a maintainer, I want the section collapsed, so that the notes stay readable.
3. As a maintainer, I want only meaningful size changes shown, so that the section isn't noise.
4. As a maintainer, I want the section to compare against the same release as the changelog, so that the two never disagree.
5. As a maintainer, I want the section omitted when there's no previous manifest, so that first releases are clean.
6. As a reviewer, I want `letsgo diff --format md` for PR comments, so that I can paste a diff.
7. As a script author, I want `letsgo diff --format json`, so that I can automate on it.
8. As a maintainer, I want to be able to disable the section, so that I stay in control of the notes.

## Implementation Decisions

- The base is `semver.Previous`
  ([ADR-0011](../adr/0011-previous-release-by-kind.md)).
- API changes are excluded, because the changelog already shows them.
- Sizes are shown for changes over 1%, at most 5, largest first.
- Deps show both direct and indirect changes.
- The opt-out is `disable diff-notes`, via #26.
- One `diff.Result` feeds three renderers: text, md and json (with
  `"schema": 1`).

## Testing Decisions

- Golden output for each format.
- A test that the notes base equals the changelog base.
- An omitted-section test for a first release.

## Out of Scope

- Linked-package or symbol-level diffs.
- Diff-based gates.

## Further Notes

- The section sits before the #34 and #35 fingerprint block.
