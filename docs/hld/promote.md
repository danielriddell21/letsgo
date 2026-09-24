# HLD: prerelease channels and `letsgo promote`

Status: proposal. Nothing here is implemented.

| | |
|---|---|
| Issue | [#29](https://github.com/danielriddell21/letsgo/issues/29) |
| Epic | [#38](https://github.com/danielriddell21/letsgo/issues/38) |
| PRD | [prd/promote.md](../prd/promote.md) |
| PBS | [pbs/promote.md](../pbs/promote.md) |
| ADRs | [ADR-0009](../adr/0009-promotion-rebuilds-at-final-tag.md), [ADR-0010](../adr/0010-promote-creates-new-release.md), [ADR-0011](../adr/0011-previous-release-by-kind.md) |

This combines the issue body and its four follow-up comments. Where they
disagree, the latest comment wins: promote **creates a new release** and
**always restores the RC to prerelease first**.

## Today

- A `-rc.1` tag is detected (`release prerelease=auto`) and the GitHub release
  is marked as a prerelease.
- The image gets `1.3.0-rc.1` and never `latest` (`internal/release/image.go:196`).
- **Bug:** the brew formula is written on prereleases. Only drafts are skipped
  (`cmd/letsgo/publish.go:51`), so an RC replaces the stable formula for every
  brew user.
- "Previous release" is computed two ways, and neither knows about
  prereleases: `discover.PreviousTag` (git describe, prereleases included) and
  `semver.Latest` (the highest tag, which is wrong for a backport).
- There is no promotion and no channel tags, and `selfupdate` only knows the
  latest release.

## Decisions

| question | decision |
| --- | --- |
| promotion model | **rebuild at the final tag**: the binary reports `1.3.0`, not `1.3.0-rc.1` |
| who creates `v1.3.0` | `letsgo promote` (CLI, or a workflow started by a UI flip) |
| RC required before stable? | no; direct `v1.3.0` tags still work |
| channel names | taken from the semver suffix (`-rc.N` → `rc`, `-beta.N` → `beta`), with no config |
| brew prereleases | one separate formula, `foo@next` |
| Docker | `latest` on stable; floating `1.3` and `1`; channel tags `rc` and `beta` |
| channel follow rule | newest of the channel or stable, so a beta user moves to stable when it is newer |
| GitHub release | promote **creates** `v1.3.0`; the RC release stays and is restored to prerelease |

## Phase 0: `semver.Previous`

This is one function used by both the git path and the forge path, so the two
can't disagree. It feeds the changelog, the API gate and `letsgo tag`.

| releasing | previous is | reason |
| --- | --- | --- |
| prerelease `v1.3.0-rc.2` | the highest release of any kind below it (`rc.1`) | testers want what changed in this RC |
| stable `v1.3.0` | the highest **stable** release below it | upgraders need everything since their last stable |
| backport `v1.2.9` | the highest stable below `v1.2.9` | fixes today's backport bug |

- The git path lists tags with `git tag --merged HEAD`, filtered by the #24
  prefix, and passes them to `semver.Previous`.
- API gate: a stable release is compared with the previous stable, so a break
  in `rc.1` isn't hidden by comparing `rc.2` with `rc.1`.
- `letsgo tag`: a stable proposal bumps from the previous stable; the next
  `-rc.N` bumps from the previous release of any kind.

This phase stands alone.

## `letsgo promote <rc-tag>`

Order (each step runs only if the previous one succeeded, except step 1,
which always runs):

1. **Restore the RC**: `prerelease: true`, not latest. This is unconditional,
   even if a later step fails, because the RC *is* a prerelease. GitHub's
   "latest" falls back to the previous stable until step 4.
2. **Tag** `v1.3.0` on the **RC's commit**, not HEAD.
3. **Rebuild and compare** against the RC manifest. The only allowed
   differences are the injected version string, archive names, and digests
   that follow from those. The commit, Go version, flags, dependencies,
   targets and plugin records must match. Anything else is a Fail.
4. **Create release `v1.3.0`**: rebuilt assets, `prerelease: false`,
   `make_latest: true`, and cumulative notes (see below). The manifest records
   `"promoted_from": {"tag": "v1.3.0-rc.1", "manifest_sha256": "…"}`.
5. **Brew and Docker** (below).

**Refuses** when the RC has no published release or manifest, the RC was
yanked, `v1.3.0` already exists, the release is a draft, or the tag isn't a
prerelease.

**Retry** by flipping the RC again, or with `letsgo promote <rc>` from the
CLI. Promote is idempotent up to the step that failed, like `release`.

`verify v1.3.0-rc.1` keeps working, since the RC release is never edited
beyond its prerelease flag. `verify v1.3.0` reports `promoted_from` and can
check the chain.

### Notes

```markdown
## v1.3.0
…everything since v1.2.4 (semver.Previous, stable rule)…

<details><summary>Prerelease history</summary>

### v1.3.0-rc.2
…
### v1.3.0-rc.1
…
</details>
```

The history is rebuilt from the RC tags using the same function, not copied
from the old bodies, so hand edits aren't carried over. `--append-notes`
works as it does today.

## UI trigger

Flipping an RC from pre-release to release in the GitHub UI fires
`release: released`. A documented workflow (a README example, not generated)
runs promote:

```yaml
on:
  release:
    types: [released]

jobs:
  promote:
    # `released` also fires for ordinary stable releases; only a prerelease tag promotes.
    if: contains(github.event.release.tag_name, '-')
    runs-on: ubuntu-latest
    permissions: { contents: write, id-token: write, attestations: write }
    steps:
      - uses: actions/checkout@v7
        with: { ref: '${{ github.event.release.tag_name }}', fetch-tags: true }
      - uses: actions/setup-go@v7
        with: { go-version-file: go.mod }
      - uses: danielriddell21/letsgo-action@v1
        with:
          command: promote
          args: ${{ github.event.release.tag_name }}
```

**Loop safety:**
- Edits made with `GITHUB_TOKEN` don't start workflows.
- With an App token, the `-` guard and "refuse if `v1.3.0` exists" make a
  re-trigger a no-op.
- Restoring the RC fires `prereleased`, not `released`.

**Brief window:** between the flip and step 1, GitHub shows the RC as latest.
Brew, Docker and selfupdate are unaffected, because they only move in step 5.

## Brew

- Stable formula `foo`: written **only** for stable releases. This is the bug
  fix, and it stands alone.
- `foo@next`: written on every prerelease and every stable release, pointing
  at whichever is newer.
- Yank rolls back `foo@next` the same way it rolls back `foo`.

## Docker

| release | tags pushed |
| --- | --- |
| `v1.3.0-beta.2` | `1.3.0-beta.2`, `beta` |
| `v1.3.0-rc.1` | `1.3.0-rc.1`, `rc` |
| `v1.3.0` | `1.3.0`, `1.3`, `1`, `latest`, plus `rc` and `beta` **if** 1.3.0 is newer than what they point at |

- Floating tags only move forward: a `v1.2.9` backport moves `1.2`, but not
  `1` or `latest`.
- The channel name is the first dot-separated prerelease identifier.
  `oci.Tag` already maps `+` to `_`.

## selfupdate

- `Options.Channel`: `""` means stable (today's behaviour); `"beta"` and
  `"rc"` follow those channels; `"next"` follows any prerelease.
- It selects the highest semver from stable plus matching prereleases,
  excluding drafts and yanked releases.
- `letsgo update --channel next` does the same for letsgo itself.

## Interactions

- **#24 monorepo:** channels and promotion are per prefix
  (`services/api/v1.3.0-rc.1` → `services/api/v1.3.0`); floating Docker tags
  are per image.
- **#26 features:** the Docker channel and floating tags are part of `image`,
  with no new switch.
- **Yank:** yanking a promoted stable leaves the RC alone; yanking an RC blocks
  promoting it.
- **#33 what shipped:** diffs against `semver.Previous`, the same base as the
  notes.

## Delivery

Phases, in order. Each is a vertical slice: a thin path through every layer, verified end to end. The behaviour each phase must meet is specified in the [PBS](../pbs/promote.md).

1. `semver.Previous` (user stories 11 (partial), 12)
2. Brew stable guard (user stories 1)
3. Docker floating and channel tags (user stories 3, 4)
4. `foo@next` (user stories 2)
5. selfupdate channels (user stories 13, 14)
6. Tracer bullet: `letsgo promote` from the CLI (user stories 5, 6, 7, 9, 10, 11, 15)
7. UI trigger (user stories 8)

## Open questions

- `promote --to v1.3.1` for renames? Proposed: no; the target is always the RC
  minus its suffix.
