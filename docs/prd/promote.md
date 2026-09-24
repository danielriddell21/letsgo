# PRD: Prerelease channels and `letsgo promote`

| | |
|---|---|
| Issue | [#29](https://github.com/danielriddell21/letsgo/issues/29) |
| Epic | [#38](https://github.com/danielriddell21/letsgo/issues/38) |
| HLD | [hld/promote.md](../hld/promote.md) |
| PBS | [pbs/promote.md](../pbs/promote.md) |
| ADRs | [0009](../adr/0009-promotion-rebuilds-at-final-tag.md), [0010](../adr/0010-promote-creates-new-release.md), [0011](../adr/0011-previous-release-by-kind.md) |

## Problem Statement

When I release `v1.3.0-rc.1`, letsgo overwrites my stable Homebrew formula,
so every brew user gets the RC. There's no way to turn a tested RC into
`v1.3.0` with any proof that it's the same build, no channel tags for
Docker, and no way for a selfupdating binary to follow betas. The changelog
for `v1.3.0` covers only rc.1 → rc.2, and a backport's previous release is
wrong.

## Solution

Prereleases become channels, named by their semver suffix. `letsgo promote
<rc>` rebuilds the RC's commit at the final tag, proves the build matches,
and creates a new stable release with cumulative notes. Flipping an RC to
"release" in the GitHub UI triggers promote through a documented workflow.
The flipped RC is always restored to prerelease. Brew gets `foo@next`,
Docker gets floating and channel tags, and selfupdate gets channels.

## User Stories

1. As a brew user, I want the stable formula untouched by prereleases, so that I never get an RC unasked.
2. As a tester, I want `brew install you/tap/foo@next`, so that I get the newest build of any kind.
3. As a Docker user, I want `rc` and `beta` tags, so that I can follow a channel.
4. As a Docker user, I want floating `1.3` and `1` tags that only move forward, so that I can pin a minor or major.
5. As a maintainer, I want `letsgo promote v1.3.0-rc.1` to release `v1.3.0` from the RC's commit, so that what I tested is what ships.
6. As a maintainer, I want promote to fail if the rebuild differs beyond the version, so that a changed dependency or toolchain can't slip in.
7. As a consumer, I want the stable manifest to record `promoted_from`, so that I can trace a release to its RC.
8. As a maintainer, I want to flip an RC to "release" in the GitHub UI and have promote run, so that promotion needs no terminal.
9. As a maintainer, I want the flipped RC restored to prerelease even if promote fails, so that the RC is never mislabelled.
10. As a maintainer, I want retry by flipping again or running the CLI, so that a failure is easy to recover from.
11. As a user, I want `v1.3.0`'s notes to cover everything since the last stable, with the RC history collapsed, so that upgraders see everything.
12. As a maintainer, I want a backport's previous release to be the highest stable below it, so that backport notes are correct.
13. As a library author, I want `selfupdate.Options.Channel`, so that my users can follow betas.
14. As a letsgo user, I want `letsgo update --channel next`, so that I can test letsgo prereleases.
15. As a maintainer, I want promote to refuse a yanked RC, an existing `v1.3.0` or a draft, so that promotion is safe.

## Implementation Decisions

- Rebuild at the final tag and compare
  ([ADR-0009](../adr/0009-promotion-rebuilds-at-final-tag.md)).
- A new release is created, and the RC is restored first and unconditionally
  ([ADR-0010](../adr/0010-promote-creates-new-release.md)).
- A single `semver.Previous` chooses the previous release by kind
  ([ADR-0011](../adr/0011-previous-release-by-kind.md)).
- Channels are named from the first prerelease identifier, with no config.
- The channel follow rule: newest of the channel or stable.
- The manifest gains `promoted_from: {tag, manifest_sha256}`.
- letsgo-action gains `command: promote`.

## Testing Decisions

- A table test for `semver.Previous` covering prerelease, stable and backport.
- Promote compare: version-only differences pass; a changed Go version or
  dependency fails.
- Promote ordering, with a fake forge: the RC is restored before tagging, and
  stays restored when a later step fails.
- A brew test showing prereleases don't touch `foo`.
- A Docker tag table, including the backport case.
- selfupdate channel selection, excluding drafts and yanked releases.

## Out of Scope

- `promote --to` renames.
- Editing the RC release beyond its prerelease flag.

## Further Notes

- The loop guard relies on `GITHUB_TOKEN` events not triggering workflows,
  plus the `-` check and the "v1.3.0 exists" refusal.
