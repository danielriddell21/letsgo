# PRD: `letsgo doctor`

| | |
|---|---|
| Issue | [#31](https://github.com/danielriddell21/letsgo/issues/31) |
| Epic | [#40](https://github.com/danielriddell21/letsgo/issues/40) |
| HLD | [hld/doctor.md](../hld/doctor.md) |
| PBS | [pbs/doctor.md](../pbs/doctor.md) |
| ADRs | none |

## Problem Statement

My releases fail in CI for setup reasons I could have caught locally: a
missing govulncheck (which Skips quietly), a shallow clone, a plugin digest
mismatch, or a go version that differs from `go.mod`. Each one costs a CI
run.

## Solution

`letsgo doctor` is a read-only, offline command that checks the tools and the
repository state, prints one line per check with a fix hint, and exits
non-zero on a hard failure.

## User Stories

1. As a maintainer, I want to see whether go, git, govulncheck and apidiff are found, with versions, so that I know my tools are ready.
2. As a maintainer, I want a warning when my local go differs from `go.mod`, so that I know a local build won't match CI.
3. As a maintainer, I want `letsgo.mod` errors shown with their positions, so that I can fix them quickly.
4. As a maintainer, I want plugin pin mismatches reported, so that I install the right versions.
5. As a CI author, I want a warning on a shallow clone, with a `fetch-depth: 0` hint, so that I fix my checkout.
6. As a maintainer, I want to know when the remote isn't GitHub, so that I understand why install.sh and brew are skipped.
7. As a maintainer, I want a dirty worktree flagged, so that I know `release` would refuse.
8. As a script or editor author, I want `doctor --json`, so that I can show the results elsewhere.

## Implementation Decisions

- It reuses plan's discovery. There is no second implementation.
- It is read-only, with no network calls.
- Status levels are ✓, ! and ✗; the exit code is non-zero only on ✗.
- A `require vulncheck` repository (#26) escalates a missing govulncheck to
  ✗.

## Testing Decisions

- Golden output for a healthy fixture and a broken one.
- Each check has a unit test with a faked resolver.
- A JSON schema test.

## Out of Scope

- Token and scope checks, and workflow linting.

## Further Notes

- The name follows `brew doctor` and `flutter doctor`.
