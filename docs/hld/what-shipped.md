# HLD: "What shipped" in release notes, and `diff --format`

Status: proposal. Nothing here is implemented.

| | |
|---|---|
| Issue | [#33](https://github.com/danielriddell21/letsgo/issues/33) |
| Epic | [#41](https://github.com/danielriddell21/letsgo/issues/41) |
| PRD | [prd/what-shipped.md](../prd/what-shipped.md) |
| PBS | [pbs/what-shipped.md](../pbs/what-shipped.md) |
| ADRs | [ADR-0011](../adr/0011-previous-release-by-kind.md) |

## Problem

`letsgo diff` (`internal/diff`) already compares two manifests: sizes,
dependencies, exported API and toolchain. Nothing is rebuilt or downloaded
beyond the manifests. But it only runs by hand. The release body shows the
changelog, which says what the commits *claim* changed.

## Proposal

### 1. Notes section

`releaseNotes` (`cmd/letsgo/main.go:776`) appends a collapsed section built
from `diff.Result`, comparing against the previous release's manifest:

```markdown
<details><summary>What shipped (vs v1.2.0)</summary>

| | |
|---|---|
| toolchain | go1.26.1 → go1.26.2 |
| deps | + golang.org/x/sync v0.9.0 · ↑ golang.org/x/sys v0.25.0 → v0.26.0 |
| size | linux/amd64 8.1 MB → 8.4 MB (+3.7%) |
</details>
```

- **Base:** `semver.Previous` (#29, ADR-0011), the same base as the changelog,
  so the two never disagree about "since when".
- **API is excluded**, because the changelog already shows it
  (`WithAPIChanges`).
- **Omitted** on a first release, when the previous release has no manifest,
  or when nothing changed.
- **Sizes:** only rows where the size changed by more than 1%, at most 5,
  largest first. `SizeKind` says binary or archive, as `diff` does.
- **Deps:** direct and indirect. `+` added, `−` removed, `↑`/`↓` moved.
- **`--snapshot`:** the recorder shows the section.
- **Opt-out:** `disable diff-notes` from #26 (or fold it under
  `disable changelog`).

### 2. `letsgo diff --format text|md|json`

- `text`: today's output, and the default.
- `md`: the same renderer as the notes section, for PR comments.
- `json`: `diff.Result` plus `"schema": 1`, per #28.

## Implementation

- `internal/diff`: add `Markdown(opts)` and `JSON()` beside the existing text
  renderer. All three render one `Result`.
- The previous manifest is fetched through the same client the changelog
  uses. A missing manifest (a release from before letsgo) omits the section.
- Golden tests for each format. A test also checks that the notes base equals
  the changelog base.

## With other proposals

- **#29:** a promoted stable release diffs against the previous stable, not
  the RC.
- **#24:** diffs within a prefix only.
- **#34/#35:** the fingerprint block sits after this section.

## Open questions

- Indirect deps in the notes, or direct only? Proposed: both, as the section
  is collapsed.

## Delivery

Phases, in order. Each is a vertical slice: a thin path through every layer, verified end to end. The behaviour each phase must meet is specified in the [PBS](../pbs/what-shipped.md).

1. `diff --format md|json` (user stories 6, 7)
2. Tracer bullet: notes section (user stories 1, 2, 3, 4, 5)
3. Opt-out (user stories 8)
