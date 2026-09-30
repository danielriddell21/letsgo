# letsgo

A release tool for Go. Only Go.

> **Status: early.** letsgo releases itself, and reproducibility is proven
> across Linux, macOS and Windows on every push.

It builds your Go project, refuses to ship it if it's wrong, publishes it
reproducibly, and lets anyone prove afterwards that the binaries match the
source.

**GoReleaser is a distribution tool. letsgo is an integrity tool.** GoReleaser's
job is getting your software to many places — Homebrew, Docker, deb, rpm, snap,
AUR, Discord. It's very good at that. letsgo's job is proving a release is what
it claims to be and never leaving it half-finished. If you need deb packages,
snap or a dozen package managers, use GoReleaser for that repo.

## Install

```sh
go install github.com/danielriddell21/letsgo/cmd/letsgo@latest
```

Or with the installer letsgo generated for its own release, which carries each
archive's SHA-256 and fails rather than installing a substituted one:

```sh
curl -fsSL https://github.com/danielriddell21/letsgo/releases/latest/download/install.sh | sh
```

Or from the tap. letsgo writes the formula from the manifest it just
published, so what brew installs is the archive the release attests to:

```sh
brew install danielriddell21/tap/letsgo
```

## Usage

```
letsgo plan [--explain] [--json] [--publish]  resolve and check a release without performing one
letsgo build [--snapshot] [-o dir]     build every artifact into dist/ without publishing
letsgo release [--draft] [-o dir]      build and publish, resumably
letsgo release --snapshot              rehearse a release without publishing
letsgo verify [tag] [--json] [--words]  rebuild a published release and compare it
letsgo doctor [--json]                 diagnose tools and repository state, read-only
letsgo audit [<tag>]                   re-check published releases against today's vulnerability database
letsgo diff <from> [to] [--format text|md|json]  compare two releases: size, dependencies, API
letsgo tag [--major|--minor|--patch|--pre|--json]  work out the next version and tag it
letsgo promote <rc-tag>                rebuild a prerelease as a stable release
letsgo yank <tag> [--reason "..."]     retract a release, including the go.mod directive
letsgo update [--check]                update letsgo itself, verified against its manifest
letsgo plugin install [<name>]         install a plugin, verified against its manifest (or every pin, with none)
letsgo plugin list [--json]             the plugins this repository pins, and what is installed
letsgo plugin prune                    remove store entries no pin in this repository references
letsgo features [--json]               the feature catalogue: what can be disabled or required
letsgo fmt [file|-]                    format letsgo.mod; - reads stdin, writes to stdout
letsgo lsp [--restricted]              serve letsgo.mod over stdio JSON-RPC, for an editor
letsgo version                         print the version (also --version)
```

There is usually no config file — project name, module path, main packages, repo
owner and version are all derived from `go.mod`, `git`, and your directory
layout. `letsgo plan` resolves and checks all of it in about two seconds without
building anything, so a misconfigured release fails before the cross-compile
rather than after it.

```sh
cd ~/code/foo
git tag v1.3.0
letsgo plan            # no side effects; --explain shows where each value came from
letsgo release         # idempotent, so a failed run resumes rather than restarts
letsgo verify v1.3.0   # rebuild it and check it against what was published
```

## Configuration

`letsgo.mod` is only needed to depart from what letsgo derives. Optional
behaviours are switched off with `disable` and made mandatory with `require`;
`letsgo features` lists every name each directive accepts. Integrity features
(the reproducible build, source archive, manifest, checksums) cannot be
disabled, and every departure is recorded in the manifest and shown by
`letsgo verify`.

```
disable sbom proxy-warm
require vulncheck api-gate
```

A `require`d feature that cannot run fails the plan instead of skipping.

Plugin configuration lives in `.letsgo/<plugin>.mod` (a legacy root-level
`<command>.mod` is still read, with a warning). Machine settings that must not
change what a release is — the Go and git to use, the build cache, the plugin
store, the module proxy, a `token-command` credential helper — go in a global
`config.mod` under the user config directory (or `$LETSGO_CONFIG`). It is
checked separately, so a release directive there is an error rather than a
silent difference between your laptop and CI.

Plugins are installed into a content-addressed store keyed by the digest
`letsgo.mod` pins, so different repositories can pin different versions on one
machine. `letsgo plugin install` with no name installs every pin.

## After a release

The release body ends with a collapsed "what shipped" section comparing the
manifest against the previous release: size changes, dependency bumps and
toolchain changes (`disable diff-notes` removes it). `letsgo diff` renders the
same comparison for any two releases.

Before any asset is attached, letsgo primes the module proxy and checks that
`sum.golang.org` agrees with the source archive it built (`disable sumdb` skips
this; `require sumdb` makes an unreachable database fatal). Later, `letsgo audit`
re-runs `govulncheck` against a published release's own source and records the
result in `audit.json` on that release, which `letsgo verify` prints. An
immutable release cannot take the extra asset, so it is skipped with a note.

## In GitHub Actions

[letsgo-action][action] installs letsgo and runs it on a runner:

```yaml
      - uses: actions/setup-go@v7
        with:
          go-version-file: go.mod

      - uses: danielriddell21/letsgo-action@v1
```

Go is a prerequisite rather than something the action installs: the Go version
changes the bytes you ship, so choosing it belongs to the workflow that knows
which one the project releases with. The action installs the latest letsgo by
default; pin `version:` to a tag when the release has to be reproducible.

[action]: https://github.com/danielriddell21/letsgo-action

## Promoting a prerelease

`letsgo promote v1.3.0-rc.1` rebuilds an RC at its own commit and publishes it
as `v1.3.0`: the RC release stays, restored to a prerelease, and the new
release records which RC it was promoted from. Nothing about the RC's tag,
assets or notes changes.

Flipping an RC from pre-release to release in the GitHub UI fires
`release: released`, which a workflow can turn into a promotion:

```yaml
on:
  release:
    types: [released]

jobs:
  promote:
    # released also fires for ordinary stable releases; only a prerelease tag promotes.
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

Editing the release with the default `GITHUB_TOKEN` doesn't start workflows;
use an App token if the flip should trigger this one automatically. Either
way a re-trigger is a no-op: promote refuses once `v1.3.0` already exists.

## Self-update

Any binary released with letsgo gets verified self-update for the cost of an
import. The release manifest records each archive's digest and the digest of
the binary inside it, so the updater checks what it downloaded twice and
refuses anything that does not match:

```go
update, err := selfupdate.Check(ctx, selfupdate.Options{
	Repo:    "you/tool",
	Current: version,
})
if err != nil || update == nil {
	return err // a nil update means the running binary is current
}
return update.Apply(ctx)
```

Nothing is written until `Apply`, so a program can offer the update rather than
take it. `letsgo update` is the same package, pointed at letsgo.

## Documentation

Full documentation lives in the [letsgo wiki](https://github.com/danielriddell21/letsgo/wiki) —
the commands, the config format, the reproducibility guarantee, publishing to
Homebrew and container registries, [plugins][], the [GitHub Action][action],
migrating from GoReleaser, and the design rationale behind all of it.

[plugins]: https://github.com/danielriddell21/letsgo/wiki/Plugins
