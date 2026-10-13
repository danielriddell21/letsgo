# ADR-0028: The installer checks the manifest, shows its fingerprint, and leaves a record

- Status: accepted
- Date: 2026-10-12
- Related: [ADR-0014](0014-fingerprints-from-manifest-digest.md), [ADR-0015](0015-easter-eggs-render-verify-result.md)

## Context

`install.sh` carries every archive's digest, so a substituted archive is
refused. It ended there: a version string, and nothing a person could compare
with anyone else. The release page, `letsgo verify` and the dashboard all show
one fingerprint for a release, the randomart and PGP words of the manifest
digest, but the installer, the one thing most users run, did not.

Two smaller gaps sat beside it. Nobody could see what the installer would do
without letting it do it. And once a binary was installed, nothing remembered
how it got there, so `letsgo update` would overwrite a Homebrew install and
nothing could tell whether the binary was still the one that was checked.

## Decision

- The installer carries the manifest's digest, fetches `letsgo.json` first and
  checks it like an archive. A substituted manifest stops the install before
  any archive is downloaded.
- With `randomart` on, a successful install ends with the manifest
  fingerprint: the release page's block, drawn at generation time from the
  same digest (ADR-0014). It is printed only after every check has passed, so
  a failed install shows none (ADR-0015). `disable randomart` drops it here as
  everywhere.
- `install.sh -n` is a dry run: it checks the manifest, lists each archive and
  digest it would install, shows the fingerprint, and downloads no archive and
  writes nothing.
- Beside each binary the installer writes `.<binary>.install.json`, schema 1:
  how it was installed, the release, the manifest, archive and binary
  digests. `selfupdate` rewrites it when it replaces that binary and never
  creates one; `letsgo doctor` compares the running letsgo with it.
- `selfupdate` refuses to replace a binary inside a Homebrew Cellar and names
  the `brew upgrade` to run instead.

## Consequences

- An attacker who replaces `install.sh` can print any picture, but not the one
  an honest `letsgo verify` or the dashboard shows for the real release. The
  fingerprint makes that comparison a glance.
- The installer downloads one more small file. A release with no manifest
  digest to give it (none today) would skip the check and the fingerprint.
- The record is a file letsgo reads back, so its schema joins the stability
  promise.
- A dotfile appears beside each installed binary. It holds no secret.

## Alternatives considered

- **Rendering the randomart in shell.** Rejected: a drunken-bishop walk in
  POSIX sh is slow and a second implementation to keep in step. The picture is
  a pure function of a digest the generator already has.
- **Showing the fingerprint without fetching the manifest.** Rejected: it
  would vouch for a manifest nobody checked, which ADR-0015 forbids.
- **One record per directory, or under the user's data directory.** Rejected:
  binaries from different projects share `/usr/local/bin`, and a record found
  from the executable's own path needs no index to keep in step.
- **Rebuilding from source at install time.** Deferred: the most thorough
  check, but it needs the exact Go toolchain and does not fit in POSIX sh.
