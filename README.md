# letsgo

A release tool for Go. Only Go.

> **Status: design stage.** There is no code yet — nothing to install, nothing
> to run. This README describes the tool we intend to build. The terminal
> sessions below are the *intended* experience, not recordings. The full
> rationale, including the alternatives we rejected, lives in
> [DESIGN.md](DESIGN.md).

---

## What it is

A release tool that builds your Go project, refuses to ship it if it's wrong,
publishes it reproducibly, and lets anyone prove afterwards that the binaries
match the source.

**GoReleaser is a distribution tool. letsgo is an integrity tool.** GoReleaser's
job is getting your software to many places — Homebrew, Docker, deb, rpm, snap,
AUR, Discord. It's very good at that. letsgo's job is proving a release is what
it claims to be and never leaving it half-finished. The two overlap on "makes a
GitHub release," but they optimise different things.

If you need Docker images or deb packages, use GoReleaser for that repo. We
expect both tools to coexist.

---

## Envisaged usage

### Zero config

No config file. Everything is derived from `go.mod`, `git`, and your directory
layout.

```
$ cd ~/code/foo
$ git tag v1.3.0
$ letsgo plan

letsgo 0.1.0 · foo v1.3.0 · commit 9f2ab1c

  gates
    ✓ worktree clean
    ✓ tag v1.3.0 is semver, not yet released
    ✓ module path github.com/danielriddell21/foo agrees with tag major version
    ✓ ldflags symbols exist — main.version, main.commit, main.date
    ✓ smoke test — ./foo --version printed "foo 1.3.0"
    ✓ govulncheck — no reachable vulnerabilities
    ✓ apidiff — API is backward compatible with v1.2.3
    ✓ GITHUB_TOKEN valid (contents:write)

  artifacts (6)
    foo_1.3.0_linux_amd64.tar.gz
    foo_1.3.0_linux_arm64.tar.gz
    foo_1.3.0_darwin_amd64.tar.gz
    foo_1.3.0_darwin_arm64.tar.gz
    foo_1.3.0_windows_amd64.zip
    foo_1.3.0_source.tar.gz
    + SHA256SUMS, letsgo.json

  plan ok in 1.9s · run `letsgo release` to publish
```

Nothing has been built yet. `plan` has no side effects and is designed to
finish in about two seconds.

### When the plan fails, nothing was built

```
$ letsgo plan

  ✗ apidiff — v1.3.0 is a minor bump, but the API is not backward compatible
      removed  func (*Client) Do(context.Context, *Request) (*Response, error)

    a breaking change requires v2.0.0 and a /v2 module path suffix
    override: letsgo release --allow-breaking

  plan failed in 2.1s · nothing was built
```

That last line is the point. The common failure mode of release tooling is
discovering a problem *after* a six-minute cross-compile.

### Releasing

```
$ letsgo release

  ✓ plan                                  1.9s
  ✓ built 5 targets                      11.2s
  ✓ archived and checksummed              0.8s
  ✓ created release v1.3.0
  ✓ uploaded 8 assets                     9.4s
  ✓ attested provenance
  ✓ warmed proxy.golang.org

  released in 24s
  https://github.com/danielriddell21/foo/releases/tag/v1.3.0
```

### Re-running after a failure

Every step is idempotent and keyed by content digest, so a re-run does only
what's left:

```
$ letsgo release          # the previous attempt died mid-upload

  ✓ plan                                  1.8s
  ✓ 5 targets from cache                  0.3s
  ✓ 6 of 8 assets already uploaded and matching
  ✓ uploaded 2 assets                     2.1s

  released in 4s
```

There is no state in which a release is half-published and unrecoverable.

### Proving a release

Because builds are byte-for-byte reproducible, anyone can rebuild a published
release and check it against what was actually shipped:

```
$ letsgo verify v1.3.0

  ✓ fetched manifest from release
  ✓ rebuilt 5/5 targets at commit 9f2ab1c
  ✓ all digests match published assets
  ✓ dependency graph matches recorded go.sum
  ✓ provenance attestation valid (github-actions, danielriddell21/foo)

  v1.3.0 verified
```

You can run this against your own releases. So can anyone else, on hardware you
don't control.

### Seeing what actually changed

Commit messages are a human-authored account of a change. This reads the
artifacts:

```
$ letsgo diff v1.2.3 v1.3.0

  binary size
    linux/amd64     12.4 MB → 16.8 MB   +4.4 MB  (+35%)  ⚠ over budget
    darwin/arm64    12.1 MB → 16.5 MB   +4.4 MB  (+36%)  ⚠ over budget

  dependencies
    + github.com/some/large-dep v1.4.0
    ~ golang.org/x/net v0.21.0 → v0.23.0

  api
    + func WithTimeout(time.Duration) Option

  toolchain
    go1.23.4 → go1.24.7
```

Size and dependency deltas come from two JSON manifests — no rebuild, no
downloads.

### Config, if you ever need it

The config file is usually absent. When it exists it's written in the same
directive syntax as `go.mod`, because you already read that fluently:

```
// letsgo.mod

project foo

build (
    linux/amd64
    linux/arm64
    darwin/arm64
)

archive (
    README.md
    LICENSE
)

budget linux/amd64 15MB

brew danielriddell21/homebrew-tap
```

Run `letsgo plan --explain` to see which defaults were inferred and from what
evidence. Zero config only feels safe if the inference is inspectable.

### In CI

```yaml
name: release
on:
  push:
    tags: ["v*"]

permissions:
  contents: write
  id-token: write        # provenance attestation
  attestations: write

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4        # no fetch-depth: 0 needed
      - uses: actions/setup-go@v5
        with: { go-version: stable }
      - run: letsgo release
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

A shallow clone is fine. If the changelog needs history we don't have, we fall
back to the GitHub compare API instead of failing.

### Coming from GoReleaser

Mostly deletion. A typical ninety-line `.goreleaser.yaml` becomes this, in
full:

```
brew you/homebrew-tap
```

Everything else it said — `CGO_ENABLED=0`, `-trimpath`, the target matrix, the
ldflags, the archive naming, the Windows zip override, the checksum file, the
changelog filters — is already the behaviour.

There is no `migrate` command. A converter for a config that mostly restates
defaults earns little, and the parts that would need one are the parts letsgo
deliberately does not do, where the answer is which tool to use for that
repository rather than how to translate a key.

[MIGRATING.md](MIGRATING.md) has the full mapping and, more usefully, the
behaviour changes to expect on the first release — including one worth knowing
in advance: a wrong `-X main.version` path now fails the release instead of
silently shipping a binary that reports `dev`.

---

## Design philosophy

**Zero config is the primary path.** Project name, module path, main packages,
repo owner, and version are all derivable. A config file is for exceptions, not
for restating what we can already see.

**Fail in two seconds, not six minutes.** Everything resolvable is resolved
before anything expensive runs. This is a sequencing decision more than a
feature, but it's the one you'd feel most often.

**Deterministic by construction.** Reproducibility isn't a flag you enable.
It's the only mode, and every knob — `-trimpath`, commit-derived timestamps,
sorted archive entries, zeroed gzip headers — is set correctly by default.
Everything interesting downstream depends on it.

**Every operation is idempotent.** Re-running is always safe and always cheap.
Because artifacts are reproducible, "is this already uploaded and correct?" is a
question we can recompute from content rather than bookkeeping we have to trust.

**Every gate has an override, and every override is visible.** Checks that block
a release are on by default, but each can be disabled explicitly, and `plan`
always reports which ran and which were skipped. A tool that blocks your release
with no escape hatch gets abandoned. A tool that skips checks silently stops
being trusted. `--allow-vulnerable` records the accepted advisory IDs in the
release manifest rather than hiding them.

**Small.** Eight verbs, no dependencies, one static binary. This tool signs and
publishes your releases, so its attack surface is part of its threat model.

**Go-only is a feature, not a limitation.** We assume the toolchain, the module
graph, the vulnerability database, and the conventions. That assumption is where
all the leverage is — reproducible-by-default is only tractable because we don't
also build container images, and semver enforcement is only possible because Go
has an API surface we can diff.

### The trade we're making

Every advantage above is paid for by something we gave up. The non-goals aren't
losses we tolerate; they're what funds the wins.

---

## What this deliberately doesn't do

- **Container images.** `docker buildx` is better at it. We won't wrap it.
- **snap, scoop, AUR, krew, nix, deb, rpm.**
- **Announcements** to Slack, Discord, Mastodon, email.
- **A plugin system.** The moment plugins exist, the config surface reopens.
- **Non-Go languages.**
- **Monorepos**, until there's one to test against.

Each of these is a place where GoReleaser is the better tool.

---

## Moving forward

### Roadmap

**M0 · Foundation**
Config parser, discovery, target matrix, deterministic archive writers,
`SHA256SUMS`, `letsgo build`, `plan --explain`.

Plus the reproducibility test suite — same commit built twice on one machine,
then built on Linux and macOS runners and compared byte for byte. **This is the
project's go/no-go gate.** If cross-machine reproduction can't be made to work,
`verify` can't ship, and without `verify` this is just a smaller GoReleaser. We
find that out first, before building anything on top of it.

**M1 · The release path**
`letsgo plan` with configuration gates and the smoke test. Changelog. GitHub
release creation and asset upload with resume. Manifest. Stable source archive.
Module proxy warm-up. `--snapshot`. `letsgo release`.

**M2 · Verification**
`letsgo verify`. OIDC provenance attestation. Build cache. The `govulncheck` and
`apidiff` gates, plus the API-derived changelog section that falls out of the
latter.

**M3 · Adoption**
A migration guide rather than a converter. Homebrew tap generation. Generated
`install.sh`. `letsgo diff` and size budgets.

*At the end of M3 letsgo can replace GoReleaser for the repos it targets, and
does several things GoReleaser cannot.*

**M4 · Later, if warranted**
`letsgo yank` — automating Go's `retract` directive, including the part people
get wrong, that a retraction only takes effect once published in a *later* tag.
SBOM. GitLab and Gitea. An embeddable `selfupdate` package so any binary
released with letsgo gets verified self-update for free.

### Questions still open

These are recorded in [DESIGN.md §19](DESIGN.md) and are worth settling before
the code that depends on them exists:

- **Config format.** `letsgo.mod` in `go.mod` syntax is the recommendation, with
  five alternatives considered and rejected. Cheap to change now, expensive
  later.
- **Is the API-compatibility gate default-on?** It's the most opinionated check
  in the tool and the most likely to irritate before we know its false-positive
  rate. Default-on with `--allow-breaking` is proposed; default-warn for the
  first release is defensible.
- **Snapshot version scheme.** `<last-tag>-next+<short-sha>` needs to sort
  correctly under semver against both its neighbours.
- **Does `letsgo build` require a tag,** or imply snapshot semantics on an
  untagged `HEAD`? Leaning toward the latter.

### How we'd like this to change

Two commitments that constrain future features more than they enable them:

**The config surface should shrink over time, not grow.** If a repo needs a new
config option, the first question is whether we could have inferred it. A
growing config file is the failure mode we're specifically trying to avoid, and
every option added is a small admission that inference wasn't good enough.

**New gates are welcome; new publishing targets mostly aren't.** Checks that
catch a class of mistake Go lets you make silently are exactly on-thesis.
Another packaging backend is how a focused tool becomes an unfocused one — and
GoReleaser already occupies that ground well.

---

## License

TBD.
