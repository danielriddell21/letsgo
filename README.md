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
letsgo plan [--explain] [--json] [--publish] [--diff [--exit-code]] [--format md] [-out file]  resolve and check a release without performing one
letsgo build [--snapshot] [-o dir]     build every artifact into dist/ without publishing
letsgo release [--draft] [-o dir]      build and publish, resumably
letsgo apply [file] [-auto-approve]   publish a release as a saved plan agreed it; with no file, plan it, show it and ask
letsgo plan -yank <tag> [-out file]    show what retracting a release would change
letsgo release --snapshot              rehearse a release without publishing
letsgo verify [tag] [--json] [--words]  rebuild a published release and compare it
letsgo doctor [--json]                 diagnose tools and repository state, read-only
letsgo audit [<tag>]                   re-check published releases against today's vulnerability database
letsgo diff <from> [to] [--format text|md|json]  compare two releases: size, dependencies, API
letsgo promote <rc-tag>                rebuild a prerelease as a stable release
letsgo yank <tag> [--reason "..."]     retract a release, including the go.mod directive
letsgo tag [--major|--minor|--patch|--pre|--json]  work out the next version and tag it
letsgo update [--check]                update letsgo itself, verified against its manifest
letsgo plugin install [<name>]         install a plugin, verified against its manifest (or every pin, with none)
letsgo plugin list [--json]            the plugins this repository pins, and what is installed
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

`letsgo.mod` is only needed to depart from what letsgo derives — optional
behaviours switched off with `disable`, made mandatory with `require`, plugins
pinned by digest, a Homebrew tap or container image turned on. Machine
settings that must not change what a release is — the Go and git to use, the
build cache, an opt-in `update-check` notice — go in a separate global
`config.mod` instead. See [Configuration][], [Features][] and
[Global config][] in the wiki.

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
which one the project releases with. See [GitHub Action][] for inputs,
outputs, permissions and approval-gated releases.

## Agreeing a release before making it

```
letsgo plan -out release.plan    # build, read the forge, show and save what would change
letsgo apply release.plan        # rebuild, refuse unless it matches the plan, then publish
```

The plan holds digests, not bytes. `apply` rebuilds and compares the manifest
before any write, so a changed toolchain or dependency between the two steps is
caught before anything is published. See [Plan and apply][] for the plan
format, yank plans and an approval-gated release in CI.

## After a release

The release body ends with a collapsed "what shipped" section comparing the
manifest against the previous release, `letsgo audit` re-checks a shipped
release against today's vulnerability database, and `letsgo promote` turns a
prerelease into the stable release it was rehearsing. See [What shipped][],
[Audit][] and [Prereleases][].

## Self-update

Any binary released with letsgo gets verified self-update for the cost of an
import:

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
take it. `letsgo update` is the same package, pointed at letsgo — see
[Self-update][].

## Documentation

Full documentation lives in the [letsgo wiki][wiki]: every command, monorepo
releases, global config, editor support, publishing to Homebrew and container
registries, [plugins][], the [GitHub Action][action], migrating from
GoReleaser, and the design rationale behind all of it.

[wiki]: https://github.com/danielriddell21/letsgo/wiki
[action]: https://github.com/danielriddell21/letsgo-action
[plugins]: https://github.com/danielriddell21/letsgo/wiki/Plugins
[Configuration]: https://github.com/danielriddell21/letsgo/wiki/Configuration
[Features]: https://github.com/danielriddell21/letsgo/wiki/Features
[Global config]: https://github.com/danielriddell21/letsgo/wiki/Global-Config
[GitHub Action]: https://github.com/danielriddell21/letsgo/wiki/GitHub-Action
[Plan and apply]: https://github.com/danielriddell21/letsgo/wiki/Plan-Apply
[What shipped]: https://github.com/danielriddell21/letsgo/wiki/What-Shipped
[Audit]: https://github.com/danielriddell21/letsgo/wiki/Audit
[Prereleases]: https://github.com/danielriddell21/letsgo/wiki/Prereleases
[Self-update]: https://github.com/danielriddell21/letsgo/wiki/Self-update
