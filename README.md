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

## Usage

```
letsgo plan [--explain] [--publish]    resolve and check a release without performing one
letsgo build [--snapshot] [-o dir]     build every artifact into dist/ without publishing
letsgo release [--draft] [-o dir]      build and publish, resumably
letsgo release --snapshot              rehearse a release without publishing
letsgo verify [tag]                    rebuild a published release and compare it
letsgo diff <from> [to]                compare two releases: size, dependencies, API
letsgo tag [--major|--minor|--patch]   work out the next version and tag it
letsgo yank <tag> [--reason "..."]     retract a release, including the go.mod directive
letsgo fmt [file]                      format letsgo.mod
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

## Documentation

Full documentation lives in the [letsgo wiki](https://github.com/danielriddell21/letsgo/wiki) —
the commands, the config format, the reproducibility guarantee, publishing to
Homebrew and container registries, migrating from GoReleaser, and the design
rationale behind all of it.
