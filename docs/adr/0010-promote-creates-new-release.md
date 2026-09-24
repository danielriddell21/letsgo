# ADR-0010: Promote creates a new release, and the RC is restored to prerelease first

- Status: proposed
- Date: 2026-09-24
- Issue: [#29](https://github.com/danielriddell21/letsgo/issues/29)
- HLD: [hld/promote.md](../hld/promote.md)

## Context

A maintainer can start a promotion by flipping an RC from pre-release to
release in the GitHub UI (the `release: released` event). Promote then either
**edits that release in place** (re-pointing its tag, replacing its assets),
or creates a new one.

## Decision

- Promote **creates** a new `v1.3.0` release, marked latest, with cumulative
  notes and a collapsed prerelease history.
- Its **first** step, run unconditionally, restores the flipped RC to
  `prerelease: true`, not latest. That happens even if promote later fails,
  because the RC *is* a prerelease.
- Order: restore RC → tag → rebuild and compare → create stable release →
  brew and Docker.
- A workflow guarded by `contains(tag, '-')` runs promote on
  `release: released`.

## Consequences

- The RC release survives, so `verify v1.3.0-rc.1` keeps working, and
  `promoted_from` points at something that still exists.
- There's no asset-swap window and no 404s.
- Restoring the RC fires `prereleased`, not `released`, so it can't start a
  loop.
- Between the flip and the restore, GitHub briefly shows the RC as latest.
  Brew, Docker and selfupdate are unaffected.

## Alternatives considered

- **Re-point in place** (an earlier draft on #29). Rejected: the RC release
  disappears, assets are replaced under users, and the RC manifest has to be
  preserved as an extra asset.
