# PBS: "What shipped" in release notes

> Product-based specification: what the product must do, stated so it can be tested.
> [PRD](../prd/what-shipped.md) · [HLD](../hld/what-shipped.md) · Issue [#33](https://github.com/danielriddell21/letsgo/issues/33) · ADR [0011](../adr/0011-previous-release-by-kind.md)

## Scope

A manifest-diff section in release notes, and markdown and JSON output for
`letsgo diff`.

## Interfaces

| surface | specification |
| --- | --- |
| notes | a collapsed `<details><summary>What shipped (vs <prev>)</summary>` table with rows for toolchain, deps and size |
| CLI | `letsgo diff <from> [to] --format text\|md\|json` (default `text`) |
| JSON | `{schema: 1, from, to, toolchain, dependencies: [...], sizes: [...], size_kind, api: [...]}` |
| config | `disable diff-notes` (#26) |

## Requirements

| ID | requirement | story |
| --- | --- | --- |
| WS-1 | The notes section MUST compare against the same previous release as the changelog. | 4 |
| WS-2 | The section MUST be collapsed. | 2 |
| WS-3 | The section MUST show the toolchain change, dependency changes (added, removed, changed, direct and indirect) and size changes. It MUST NOT show API changes. | 1 |
| WS-4 | Size rows MUST appear only for changes over 1%, at most 5 rows, largest first. | 3 |
| WS-5 | The section MUST be omitted when there's no previous manifest, or no changes. | 5 |
| WS-6 | `--snapshot` MUST show the section. | — |
| WS-7 | `diff --format md` MUST produce the same table as the notes section, plus API changes. | 6 |
| WS-8 | `diff --format json` MUST carry `schema: 1`. | 7 |
| WS-9 | `disable diff-notes` MUST remove the section. | 8 |

## Acceptance scenarios

1. **Given** a second release that bumped go and added one dependency, **when** it releases, **then** the notes show both changes, and the size row appears only if it moved more than 1%. (WS-3, WS-4)
2. **Given** a first release, **when** it releases, **then** there's no section. (WS-5)
3. **Given** `v1.2.9` backported after `v1.3.0`, **when** it releases, **then** the section says "vs v1.2.8". (WS-1)
4. **Given** `diff v1.0.0 v1.1.0 --format json`, **when** it runs, **then** the output parses and has `schema: 1`. (WS-8)
