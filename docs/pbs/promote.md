# PBS: Prerelease channels and `letsgo promote`

> Product-based specification: what the product must do, stated so it can be tested.
> [PRD](../prd/promote.md) · [HLD](../hld/promote.md) · Issue [#29](https://github.com/danielriddell21/letsgo/issues/29) · ADRs [0009](../adr/0009-promotion-rebuilds-at-final-tag.md), [0010](../adr/0010-promote-creates-new-release.md), [0011](../adr/0011-previous-release-by-kind.md)

## Scope

Choosing the previous release by kind, prerelease channels (brew, Docker,
selfupdate), and promoting an RC to stable from the CLI or the GitHub UI.

## Interfaces

| surface | specification |
| --- | --- |
| CLI | `letsgo promote <prerelease-tag>`, `letsgo update --channel <name>` |
| `selfupdate` | `Options.Channel string`: `""` = stable, `"<name>"` = that channel, `"next"` = any prerelease |
| manifest | `promoted_from: {tag, manifest_sha256}` |
| brew | formula `foo` (stable only), formula `foo@next` |
| Docker tags | `<version>`, `<major>.<minor>`, `<major>`, `latest`, `<channel>` |
| letsgo-action | `command: promote`, `args: <tag>` |
| channel name | the first dot-separated prerelease identifier (`rc.1` → `rc`) |

## Requirements

| ID | requirement | story |
| --- | --- | --- |
| PR-1 | The previous release of a prerelease MUST be the highest release of any kind below it. | 11 |
| PR-2 | The previous release of a stable release MUST be the highest stable release below it. | 11, 12 |
| PR-3 | The changelog, API gate and `letsgo tag` MUST all use the same previous-release function. | 12 |
| PR-4 | A prerelease MUST NOT write the stable formula `foo`. | 1 |
| PR-5 | Every release MUST write `foo@next`, pointing at the newer of the newest prerelease and the newest stable. | 2 |
| PR-6 | A prerelease MUST push `<version>` and `<channel>` Docker tags. | 3 |
| PR-7 | A stable release MUST push `<version>`, and MUST move `<major>.<minor>`, `<major>`, `latest` and each channel tag only if it's newer than the tag's current target. | 4 |
| PR-8 | `promote` MUST first set the RC release to prerelease and not latest, unconditionally, before any other step. | 9 |
| PR-9 | `promote` MUST tag `<version without suffix>` on the RC's commit. | 5 |
| PR-10 | `promote` MUST rebuild and compare with the RC manifest. The only allowed differences are the version string, archive names, and the digests those imply. | 6 |
| PR-11 | `promote` MUST create a new stable release, marked latest, and MUST NOT modify the RC's tag, assets or notes. | 5 |
| PR-12 | The stable manifest MUST record `promoted_from`. | 7 |
| PR-13 | The stable notes MUST cover everything since the previous stable, with a collapsed "Prerelease history" section. | 11 |
| PR-14 | `promote` MUST refuse a yanked RC, a draft, a non-prerelease tag, or an existing target tag. | 15 |
| PR-15 | `promote` MUST be re-runnable after a failure, resuming from the failed step. | 10 |
| PR-16 | A `release: released` event on a tag containing `-` MUST run promote; other tags MUST be a no-op. | 8 |
| PR-17 | `selfupdate` with a channel MUST choose the highest of stable plus that channel, excluding drafts and yanked releases. | 13, 14 |

## Errors and edge cases

- The compare fails: the release isn't created, the RC stays restored, and
  the exit is non-zero with the differing fields listed.
- Promote runs twice concurrently: the second one refuses, because the target
  tag exists.
- A backport `v1.2.9` released after `v1.3.0`: it moves `1.2` only.
- Build metadata (`+meta`): Docker tags map `+` to `_`.

## Acceptance scenarios

1. **Given** `v1.2.8`, `v1.3.0-rc.1`, `v1.3.0-rc.2`, **when** `v1.3.0` is promoted, **then** the notes cover `v1.2.8..v1.3.0` with rc.1 and rc.2 in the history. (PR-2, PR-13)
2. **Given** `v1.3.0` exists, **when** `v1.2.9` releases, **then** its previous release is `v1.2.8`, and Docker moves `1.2` but not `1` or `latest`. (PR-2, PR-7)
3. **Given** `v1.3.0-rc.1`, **when** it releases, **then** `Formula/foo.rb` is unchanged and `foo@next.rb` points at rc.1. (PR-4, PR-5)
4. **Given** an RC flipped to release in the UI and a Go version change injected, **when** promote runs, **then** the RC ends as a prerelease, no `v1.3.0` release exists, and the run fails with the Go version listed. (PR-8, PR-10)
5. **Given** a clean RC, **when** `letsgo promote v1.3.0-rc.1` runs, **then** `v1.3.0` is latest, its binary reports `1.3.0`, `promoted_from.tag` is the RC, and `verify v1.3.0-rc.1` still passes. (PR-9, PR-11, PR-12)
