# ADR-0024: One Published Release reader selects and fetches what the forge holds

- Status: accepted
- Date: 2026-10-01
- Issue: [#173](https://github.com/danielriddell21/letsgo/issues/173)

## Context

Six modules each pick "the" Published Release and fetch its Manifest
themselves: `verify`, `audit`, `diff`, `promote`, `yank` and `selfupdate`.
They apply different rules for scope, drafts, Yanks and prereleases, and the
manifest download-decode-hash is copied four times. `diff` ignores scope
(#165). Scoped `selfupdate` offers prereleases and Yanked releases (#164).
`audit` imports `verify` only for `FetchManifest` and file-name constants, so
`verify` duplicates them instead of importing back.

## Decision

Add a leaf package, `internal/releases`, that owns selection and fetching for
a Published Release (see CONTEXT.md).

- **Narrow entry points**, not one rule struct: `ByTag`, `Latest` and
  `PerMajor`, plus `ByTagIncludingDrafts` for `promote`, which must see a
  draft RC. The scope, draft, Yanked and prerelease filtering is shared and
  hidden behind them.
- **It owns fetching**: listing releases, and downloading, decoding and hashing
  the Manifest. A caller supplies the "only releases published by letsgo can
  be X" suffix, so existing error messages do not change.
- **Small sources, each of at most three methods**, declared in the reader and
  taken only by the entry point that needs them (`TagSource`, `ListSource`,
  `LatestSource`, `Downloader`). They return the reader's own `Published`
  struct, not `github.Release`, so the reader imports no forge package.
- **Two adapters justify the seam**: `internal/github.Client`, and
  `selfupdate`'s lightweight HTTP.
- `yank.IsRetracted` moves into the reader; `yank` keeps a one-line alias
  while callers migrate.
- `audit`'s newest-stable-per-major rule (AU-1) becomes `PerMajor`.

## Order of work

1. Extract the reader with today's behaviour, quirks included, behind
   characterization tests.
2. Move `verify`, `audit`, `diff`, `promote` and `yank` onto it.
3. Fix #165, then #164, each as its own behavioural change with a test that
   fails first.
4. Move `selfupdate` onto it last, in its own change with its own adapter.

Structural and behavioural changes never share a change.

## Consequences

- Release-selection rules live in one place and are tested as an in-process
  table over release lists.
- The `audit` → `verify` import cycle goes away.
- `selfupdate` is public and now depends on an internal package; it already
  imports `internal/manifest`, which #155 treats as the wrong direction, so
  that is settled there, not here.
- Fits [ADR-0011](0011-previous-release-by-kind.md).

## Alternatives considered

- **One `Select(releases, Rule)`** with scope, draft, retracted and channel
  flags. Rejected: a five-or-six-flag struct is an interface nearly as wide as
  the implementation behind it.
- **Selection only, fetching left to each caller.** Rejected: leaves the four
  manifest-fetch copies, which differ only in error wording.
- **Put it in `internal/verify`.** Rejected: keeps the `audit` → `verify` cycle.
- **Put it in `internal/github`.** Rejected: couples the client package to
  scope and semver rules.
