# letsgo

A release tool for Go. Only Go.

> **A release should be correct before it ships, provable at the moment it
> ships, and legible long after.**

This document is the design rationale: the decisions, the alternatives we
rejected, and the reasoning behind both. For the intended user experience and
the roadmap, see [README.md](README.md). No code exists yet.

---

## 1. Positioning: how this differs from GoReleaser

### The one-line difference

**GoReleaser is a distribution tool. letsgo is an integrity tool.**

GoReleaser's job is *get my software to many places* — Homebrew, Docker, deb,
rpm, snap, scoop, AUR, krew, Discord, Mastodon. It is very good at that, and
breadth is the correct axis for it.

letsgo's job is *prove this release is what it claims to be, refuse to ship it
if it is wrong, and never leave it half-finished*. The two overlap on "makes a
GitHub release," but they optimise different things. This is not a claim that
GoReleaser is bad software. It is a claim that there is a different axis.

### Where the advantage comes from

This is the load-bearing part of the argument, because it explains why we can
do things GoReleaser cannot, rather than merely asserting we are smarter.

> **The non-goals fund the differentiators.** They are not losses we tolerate.
> They are what pays for the wins.

- Reproducible-by-default is tractable because we only build Go. Add Docker
  layers, snap, or CGO toolchains and byte-identical output stops being
  achievable as a default.
- Symbol-checked `-X` flags are possible because we can parse the main package
  AST. That is only cheap when Go is the sole target.
- A closed template vocabulary works because there are about ten things to
  interpolate. GoReleaser needs full `text/template` precisely *because* it
  supports fifteen packaging backends with different naming rules.
- Semver enforcement, vulnerability gating, and module-path checks
  (§9) are only possible against a language with a module system, an API
  surface we can diff, and a vulnerability database keyed to it.

Every advantage below traces back to the scope cut. GoReleaser's authors did
not miss these; their scope forecloses them.

### Five scenarios

| | GoReleaser | letsgo |
|---|---|---|
| **CI dies after uploading 4 of 6 assets** | Re-run produces conflicting assets, or you delete the release and redo the whole build. Resume is a Pro feature. | Re-run skips the 4 by digest, uploads 2, finishes in seconds. |
| **Typo in an archive-name template** | Six minutes of cross-compiling, *then* the template error. Zero artifacts. | `plan` fails in about two seconds, names the bad variable, lists the valid ones. |
| **`--version` prints `dev` in production** | `-X main.version` silently no-ops on a wrong symbol path. You learn from a user's bug report. | Plan fails: `main.version not found in ./cmd/foo (did you mean main.Version?)`, and the smoke test would have caught it anyway. |
| **"Is this binary really built from the tagged source?"** | Unanswerable. Not only for third parties — *you* cannot answer it either. | `letsgo verify v1.2.3` |
| **Shallow clone in CI** | Cryptic changelog failure, so you set `fetch-depth: 0`, so every CI run is slower forever. | Falls back to the GitHub compare API. |

### Differences sorted by kind

A flat feature list overstates the case. Sorted by what kind of difference each
one actually is:

**1. Capabilities GoReleaser cannot have.**
`letsgo verify` — rebuild a published release from source and compare digests.
GoReleaser has no equivalent *because non-reproducible builds make it
impossible*. Reproducibility is not a feature here, it is the prerequisite.
Likewise the published manifest (§8), from which `install.sh`, update checkers,
mirroring, and `letsgo diff` all fall out without release-page scraping.

**2. Capabilities GoReleaser charges for.**
Resume after partial failure. Ours is also structurally better: because
artifacts are reproducible, "is this asset already correct?" is a question we
recompute from content rather than bookkeeping we have to trust.

**3. Same capability, better ordering.**
Plan/apply. All cheap validation before all expensive work. A sequencing
change rather than a new feature, but the one you would feel most often.

**4. Different defaults.**
`-trimpath`, `CGO_ENABLED=0`, commit-derived timestamps, deterministic
archives. In GoReleaser these are reachable but opt-in, and even with
`mod_timestamp` set, gzip headers remain non-deterministic.

**5. Just smaller.** *(the weak category)*
Eight verbs, a ten-line config, three dependencies. **This is not a reason to
switch.** "Smaller" is a pleasant property of a tool you already chose for
other reasons. If categories 1 through 4 do not land, this one will not carry
the argument.

### Where GoReleaser genuinely wins

Stated plainly, because a pitch that will not name its losses is not credible.

- **Packaging breadth** — nfpm (deb/rpm/apk), snap, scoop, AUR, krew, nix. We
  do none of it. Container images are the one exception, and only because a
  static Go binary on `scratch` needs no build system at all (§18).
- **Announcements** — Slack, Discord, Mastodon, email.
- **Multi-forge** — GitLab and Gitea work today; letsgo publishes to GitHub,
  behind an interface that makes a second forge an addition rather than a
  rewrite, but nobody should wait for one.
- **Maturity** — years of accumulated edge cases from a large user base. We
  will ship bugs they fixed in 2021.
- **Monorepos** — a Pro feature, but it exists. We have deferred it entirely.

If a repo needs any of these, use GoReleaser for that repo. The plan assumes
both tools coexist, not that letsgo replaces GoReleaser everywhere.

### The honest risk

The entire pitch rests on §7. If the determinism spec cannot be made to hold,
`verify` does not ship, category 1 evaporates, and what remains is "GoReleaser
but smaller" — category 5, which is not enough. That is why the determinism
test is a **gate in M0**, not a nice-to-have in M2. We find out whether this
tool has a reason to exist in the first milestone, before building a release
path on top of it.

---

## 2. Design principles

1. **Zero config is the primary path.** A config file is for exceptions.
2. **Fail in two seconds, not six minutes.** Everything resolvable is resolved
   before anything expensive runs.
3. **Deterministic by construction.** Reproducibility is not a flag. It is the
   only mode.
4. **Every operation is idempotent.** Re-running is always safe and cheap.
5. **Every gate has an override, and every override is visible.** Checks that
   block a release (§9) are default-on, but each can be disabled explicitly,
   and `plan` always reports which gates ran and which were skipped. A tool
   that blocks your release with no escape hatch is a tool you stop using; a
   tool that lets you skip checks silently is one you stop trusting.
6. **Small.** Eight verbs, three direct dependencies, one static binary.
7. **Go-only is a feature.** We assume the toolchain, the module graph, the
   vulnerability database, and the conventions. That assumption is where all
   the leverage is.

---

## 3. The three acts

Everything in this design belongs to one of three phases of a release's life.

### Act I — Correct before it ships

`letsgo plan` is not a config validator. It is a **release gate** (§9). It
refuses to ship a release that is wrong in ways Go lets you be wrong silently:
a `/v2` module path tagged `v1.4.0`, a minor bump that removes an exported
function, a binary with a known reachable vulnerability, a `-X` flag pointing
at a symbol that does not exist, a binary that does not run.

Nothing in this act requires trusting us. Each check is a thing you could have
run yourself and would not have.

### Act II — Provable at the moment it ships

Reproducible builds (§7), a machine-readable manifest (§8), digests, and OIDC
provenance attestation (§13). The release is a pure function of the commit, and
the artifact records enough to re-derive itself:

```
$ letsgo verify v1.2.3
  ✓ fetched manifest from release
  ✓ rebuilt 6/6 targets at commit 9f2ab1c
  ✓ all digests match published assets
  ✓ dependency graph matches recorded go.sum
  ✓ provenance attestation valid (github-actions, danielriddell21/foo)
```

Anyone can run that against anyone's release. This is the reason to switch.

### Act III — Legible after it ships

A release is not finished when the assets upload. `letsgo diff` (§14) answers
"what actually changed between these two releases" in terms of binary size,
dependency graph, and exported API — not commit messages. `letsgo yank` (§14)
handles the case where a release turns out to be a mistake, including the
`go.mod` `retract` directive that Go provides and nothing in this category
knows about.

---

## 4. CLI surface

The entire tool:

```
letsgo plan                 resolve, gate, and print the intended release. no side effects
letsgo build                build + archive + checksum into dist/. no publish
letsgo release              plan, build, publish. resumable, idempotent
letsgo release --snapshot   same code path; publishing swapped for a recorder
letsgo verify [tag]         rebuild from source, compare against published digests
letsgo tag                  work out the next version from the API and the commits
letsgo diff <tag> <tag>     size, dependency, and API deltas between two releases
letsgo yank <tag>           retract a bad release, including the go.mod directive
```

Plus `letsgo fmt` (formats the config) and `letsgo version`.

`letsgo plan --explain` additionally prints *why* each value was chosen — which
defaults were inferred, from what evidence, and which gates ran. Zero config
only feels safe if the inference is inspectable; without `--explain` the
primary path is indistinguishable from magic.

There is no `--rm-dist` and no matrix of `--skip-*` flags. If a skip flag
becomes necessary for anything other than a §9 gate, that is a signal the
pipeline is doing something it should not.

---

## 5. Config format

### Decision: Go's own config syntax

The config file is `letsgo.mod`, written in the line-and-block directive syntax
used by `go.mod` and `go.work`:

```
project foo

build (
    linux/amd64
    linux/arm64
    darwin/arm64
    darwin/amd64
    windows/amd64
)

ldflags -s -w

archive (
    README.md
    LICENSE
)

release prerelease=auto

brew danielriddell21/homebrew-tap
```

Rationale:

- **Zero learning cost for the audience.** This tool's users are, by
  definition, Go developers. They read this syntax fluently every day. It is
  "our own format" without being an invented one.
- **Trivially parseable.** Line-oriented with parenthesised blocks. A complete
  parser with good error messages is roughly 200 lines, which keeps the config
  dependency count at zero.
- **Strict and unambiguous.** No indentation significance, no YAML type
  coercion, no JSON comment problem. Comments use `//`.
- **Formattable.** `letsgo fmt` canonicalises it exactly as `go mod tidy` does,
  so config never drifts stylistically across repos.
- **Low blast radius.** The file is usually absent and always tiny, which is
  what makes choosing the pleasant option defensible rather than self-indulgent.

Known cost: no editor syntax highlighting out of the box. Mitigations are that
`go.mod` highlighters mostly apply by coincidence of syntax, the file is rarely
more than ten lines, and parse errors carry `file:line:col` plus a suggestion.

### Alternatives considered

| Option | Why not |
|---|---|
| TOML | Fine, but needs a dependency and adds a fourth format to a repo that already has `go.mod` |
| YAML | Familiar from GoReleaser, but inherits YAML's ambiguity, and matching their format invites matching their surface area |
| JSON + schema | No comments, noisy to hand-edit |
| HCL | Fits the plan/apply framing, but a heavy dependency for a ten-line file |
| Starlark / CUE | Now the config is a programming language. Directly against principle 1 |
| Config inside `go.mod` | Unknown directives are a hard error in the Go toolchain. Not possible |

Still open for revision before implementation starts (§18).

---

## 6. Derived defaults (the zero-config path)

| Value | Derived from |
|---|---|
| Project name | `go.mod` module path, last element |
| Repo owner/name | `git remote get-url origin` |
| Version | The git tag being released; `--snapshot` uses `<last-tag>-next+<short-sha>` |
| Main packages | `./cmd/*` if present, else the module root if it is `package main` |
| Targets | `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64` |
| Archive format | `tar.gz`, and `zip` for Windows |
| Archive name | `<project>_<version>_<os>_<arch><ext>` |
| Extra archive files | `README*`, `LICENSE*`, `CHANGELOG*` if present at the repo root |
| Version injection | `-X main.version`, `main.commit`, `main.date` — **only if those symbols exist** |

`letsgo plan --explain` prints this table populated with the actual evidence for
each row.

### Symbol-checked ldflags

Before building, we parse the main package and confirm each `-X` target symbol
exists and is a package-level `string`. If it does not, plan fails naming the
symbol. This alone fixes the most common silent failure in Go release tooling.

We also detect whether the binary already calls
`runtime/debug.ReadBuildInfo()`. Go embeds VCS data automatically, so in that
case we suggest dropping the ldflags rather than injecting redundant values.

### Template vocabulary (closed set)

`{{project}}` `{{version}}` `{{tag}}` `{{commit}}` `{{short_commit}}`
`{{date}}` `{{os}}` `{{arch}}` `{{binary}}` `{{ext}}`

No functions, no conditionals, no pipelines. Unknown names are a plan-time
error listing the valid set. If a case genuinely needs logic, that is a bug
report about a missing default, not a reason to add a template language.

---

## 7. Reproducibility specification

This section is normative. Deviating from it is a bug.

**Source of time.** `SOURCE_DATE_EPOCH` is the committer timestamp of the
commit being released. It is never `time.Now()`.

**Build.**
- `-trimpath` always.
- `CGO_ENABLED=0` unless explicitly overridden in config.
- `-ldflags "-s -w"` by default, plus version injection.
- A clean worktree is required for a real release (`--allow-dirty` exists for
  `build` and `--snapshot` only), so the commit hash is a valid cache key.
- The Go toolchain version is recorded in the manifest, because it is an input.

**tar.**
- Entries sorted by path, byte-wise.
- `Uid`/`Gid` = 0, `Uname`/`Gname` empty.
- `ModTime` = `SOURCE_DATE_EPOCH`; `AccessTime` and `ChangeTime` zeroed.
- Mode `0755` for binaries, `0644` for everything else.
- `Header.Format` pinned explicitly rather than inferred per entry.

**gzip.**
- `Header.Name` empty, `Header.ModTime` zero, `Header.OS` = 255 (unknown).
  Go's default writes both an OS byte and a modtime, which breaks
  byte-equality across machines.

**zip.**
- Entries sorted by path.
- `Modified` = `SOURCE_DATE_EPOCH` in UTC, applied identically to every entry.

**Enforcement, in two tiers.**

1. *Same-machine:* CI builds the same commit twice in different working
   directories, at different wall-clock times, with different `TMPDIR`s, and
   asserts digest equality.
2. *Cross-machine:* CI builds the same commit on Linux and macOS runners and
   asserts the cross-compiled artifacts match byte for byte.

Tier 2 is the claim that actually matters, because it is the one a third party
relies on when they run `letsgo verify` on hardware we do not control. If
either tier cannot be made to pass, the feature it covers does not ship.

---

## 8. The release manifest

Every release publishes `letsgo.json` as an asset:

```json
{
  "schema": 1,
  "project": "foo",
  "version": "1.2.3",
  "tag": "v1.2.3",
  "commit": "9f2ab1c...",
  "source_date_epoch": 1757000000,
  "builder": { "tool": "letsgo 0.1.0", "go": "go1.24.7" },
  "source": { "archive": "foo_1.2.3_source.tar.gz", "sha256": "..." },
  "modules": { "go_sum_sha256": "...", "count": 14 },
  "gates": { "vulncheck": "pass", "apidiff": "pass", "smoke": "pass" },
  "artifacts": [
    {
      "name": "foo_1.2.3_linux_amd64.tar.gz",
      "os": "linux",
      "arch": "amd64",
      "size": 4812345,
      "sha256": "...",
      "binary_sha256": "...",
      "build": {
        "flags": ["-trimpath"],
        "ldflags": "-s -w -X main.version=1.2.3 -X main.commit=9f2ab1c",
        "env": { "CGO_ENABLED": "0" }
      }
    }
  ]
}
```

Recording `size`, `go_sum_sha256`, and the gate results is what makes `letsgo
diff` (§14) nearly free: size and dependency deltas between two releases need
only the two manifests, with no rebuild.

One small file yields five things:

1. `letsgo verify` — everything needed to reproduce the build is recorded.
2. `letsgo diff` — size and dependency deltas without downloading artifacts.
3. A generated `install.sh` that selects the right asset and verifies its
   checksum, with no release-page scraping.
4. Update checkers — a client diffs its embedded version against the manifest.
5. Mirroring and air-gapped distribution.

---

## 9. The release gate

`letsgo plan` runs these before any expensive work. Each is default-on with an
explicit override (principle 5), and `plan` reports which ran and which were
skipped. Gate results are recorded in the manifest, so a consumer can see what
a release was checked against.

### Configuration and environment

1. Worktree is clean (unless `--snapshot`).
2. Tag exists, parses as semver, is not already released — or `--replace`.
3. `GITHUB_TOKEN` present, valid, carrying the scopes this release needs. One
   API call.
4. All config templates resolve against the closed vocabulary (§6).
5. Target list is valid against `go tool dist list`.
6. Homebrew tap repo is writable, if a tap is configured.

### Go-native correctness

These are the checks only a Go-only tool can afford to make, and they are where
most of the novel value sits.

7. **Major version suffix agreement.** If the module path ends in `/vN` for
   N ≥ 2, the tag must be `vN.x.y`, and vice versa. Publishing `v2.0.0` from a
   module path without a `/v2` suffix produces a release that `go get` will
   never resolve — one of the most common and most confusing Go module
   mistakes. It is a two-line check that nothing in this category performs.

8. **Exported API versus semver bump.** If the module exports packages beyond
   `main`, diff the exported API against the previous tag. A removal or
   incompatible change in a patch or minor bump fails the gate with the
   specific symbol named:

   ```
   ✗ apidiff: v1.3.0 is a minor bump, but the API is not backward compatible
       removed: func (*Client) Do(context.Context, *Request) (*Response, error)
       changed: func New(string) *Client -> func New(string, ...Option) *Client
     a breaking change requires v2.0.0 and a /v2 module path suffix
     override: letsgo release --allow-breaking
   ```

   For a library, accidentally breaking API in a minor release is the cardinal
   sin of the Go ecosystem, and the tooling to detect it
   (`golang.org/x/exp/apidiff`) has existed for years without ever being wired
   into a release tool.

9. **Known reachable vulnerabilities.** Run `govulncheck` and fail on findings
   in reachable code. Go's vulnerability database is first-party and
   govulncheck performs reachability analysis, so unlike generic SCA the signal
   is low-noise enough to gate on. The claim — *letsgo will not publish a
   binary with a known reachable vulnerability* — is a meaningful safety
   property, and it is only credible because of the reachability analysis.
   Override: `--allow-vulnerable`, which records the accepted advisory IDs in
   the manifest rather than hiding them.

10. **Symbol-checked ldflags** (§6).

11. **Smoke test.** Build the host-platform binary first and run it — by
    default `--version`, asserting the output contains the expected version
    string; configurable to any command. This catches the version-injection
    class of bug empirically rather than only structurally, and it catches a
    binary that panics on startup before six other targets are built and
    published. Skipped automatically when the host platform is not in the
    target list.

### Dependency note

`apidiff` and `govulncheck` are invoked as **external binaries**, not linked as
libraries, so neither enters our dependency graph (§16). If the binary is
absent, plan reports the gate as unavailable with the one-line `go install`
command rather than failing — an unavailable gate is a warning, a failing gate
is an error.

Output of a successful plan is the full artifact list with names, targets, and
resolved ldflags, plus the gate summary.

---

## 10. Resumability

Before uploading, the release's existing assets are listed and compared by name
and digest, falling back to size where the forge reports no digest. Matching
assets are skipped; mismatched ones are deleted and re-uploaded.

Because artifacts are reproducible, "already uploaded and correct" is a question
with a definite answer. That is what makes resume safe rather than hopeful.

There is no local state file. An earlier draft of this design had one, recording
completed steps by digest so a re-run in the same directory would be instant.
Building it showed it to be a cache of an answer the forge gives directly, and a
cache that disagrees with the server is worse than no cache at all.

**Build cache.** Binaries are cached under a content-addressed key of
`(commit, target, flags, toolchain version)`. Go's own build cache handles
incremental compilation; ours removes the re-archive and re-checksum work, so a
retried release costs seconds rather than minutes.

---

## 11. Changelog

Default behaviour, in order of preference:

1. Group by pull request where PR data is available, deduplicating
   squash-merge noise.
2. Fall back to conventional-commit grouping (`feat`, `fix`, `perf`, …).
3. Fall back to a plain commit list.

Breaking changes are detected from `!` in the type prefix and from
`BREAKING CHANGE:` trailers, and hoisted to the top. Contributors are credited.

**An API-changes section generated from the diff, not the prose.** Because §9
already computes the exported API delta, the changelog can carry a section
derived from what the code actually did rather than what the commit messages
claimed:

```
### API changes
  + func WithTimeout(time.Duration) Option
  + func (*Client) Close() error
  ~ func New(string) *Client -> func New(string, ...Option) *Client
```

Commit messages are a lossy, optional, human-authored account of a change. The
API diff is the change. Deriving release notes from the latter is obvious in
retrospect and, as far as this design is aware, unattempted.

**No `fetch-depth: 0` requirement.** On a shallow clone, fall back to the
GitHub compare API rather than failing — a direct fix for a well-known CI
papercut.

---

## 12. Supply chain

- `SHA256SUMS` published alongside artifacts.
- Build provenance attestation via GitHub OIDC — sigstore-backed, no key
  management, nothing to rotate.
- The manifest records `go.sum`'s digest, so `letsgo verify` proves the
  dependency graph as well as the output.
- A generated `install.sh` carries every archive's digest inline, so installing
  verifies against a number recorded by the build rather than one fetched from
  the same page as the archive. A `curl | sh` installer that downloads its own
  checksum file proves only that the release page is internally consistent.
- SBOM generation is deferred to M4. It is table stakes, not a differentiator.

---

## 13. A stable source archive

GitHub's automatically generated source tarballs are not guaranteed to be
byte-stable over time, and a change in how they are produced has previously
invalidated checksums that downstream packagers had already published. Anything
that pins a checksum of `https://github.com/…/archive/v1.2.3.tar.gz` — Homebrew
formulas among them — is exposed to that.

letsgo therefore publishes its **own** source archive, built with the same
deterministic writer as every other artifact (§7) and recorded in the manifest.
Anything downstream that needs to pin a source checksum — a distro package, a
vendoring mirror, a build-from-source formula — can pin ours. The cost is one
extra asset; the benefit is that a checksum we publish is a checksum we
control.

### Homebrew formulas

A tap is generated from the release that just happened, one formula per command
the module builds, each pointing at the prebuilt archives with the digests the
manifest recorded. `brew install` then unpacks a binary rather than compiling
one, and the digest it checks came from the build rather than from a checksum
file served by the same page as the archive.

Three decisions are worth stating:

- **One formula per command.** A formula installs one archive per platform, so
  a module with `alpha` and `beta` cannot express both in one. Generating
  `Formula/alpha.rb` beside `Formula/beta.rb` is what a tap is shaped to hold
  anyway; refusing the repository would have been the lazier answer.
- **The `test do` block asserts the version.** letsgo has already run the
  binary and confirmed it reports this version (§9), so the formula can assert
  the same thing rather than merely proving the binary starts. That catches a
  formula pointing at the wrong release, which is the failure a tap actually
  has.
- **An unchanged formula is not committed.** Rendering is deterministic, so
  re-running a release — after a failed upload, or a corrected changelog —
  produces identical bytes and writes nothing. A tap is someone else's
  repository; filling its history with commits that say nothing happened is a
  cost paid by whoever reads it later.

`desc` and `license` come from the forge's own description of the repository
rather than from two new config directives, and are omitted when it has none.
Inventing either would be worse than leaving them out.

The tap is checked during `plan`, before anything is built: a release that
succeeds and then cannot write the formula has left the two out of step, which
is worse than not starting. Inside GitHub Actions the check can only warn — the
workflow token cannot write to another repository at all, so a tap needs a PAT
or an App token, and the repository endpoint will not describe either.

---

## 14. After the release

### `letsgo diff <tag> <tag>`

Answers "what actually changed between these two releases" in terms of
artifacts rather than commit messages:

```
$ letsgo diff v1.2.3 v1.3.0
v1.2.3 -> v1.3.0

binary size
  linux/amd64    12.4 MB -> 16.8 MB   +35%
  darwin/arm64   12.1 MB -> 16.5 MB   +36%

dependencies
  + github.com/some/large-dep v1.4.0
  ~ golang.org/x/net v0.21.0 -> v0.23.0

api
  + example.com/foo: func WithTimeout(time.Duration) Option

toolchain
  go1.23.4 -> go1.24.7
```

Every line comes from the two manifests. Nothing is rebuilt and nothing is
downloaded, which makes this feature nearly free once §8 exists — and means it
works against a project that is not checked out, or between two releases of
someone else's. Either side may equally be a local `letsgo.json`, so "what did
the build I just ran do to the binary" is the same command.

Binary size regressions are a real and under-tooled problem in Go, and a single
added dependency is usually the cause — putting both on the same screen makes
the culprit obvious. The toolchain line is listed last and is often the
explanation, because a compiler change moves every target at once and no
dependency accounts for it.

The size compared is the **binary**, not the archive, wherever both manifests
record one: archive size moves with the compressor as well as with the code.
Against a release published before letsgo recorded binary size, the comparison
falls back to archive size and says so rather than mixing the two.

**Size budgets.** Optional config (`budget linux/amd64 15MB`) caps a target's
binary. The size is parsed at plan time — a malformed one, or a budget naming a
target the release does not build, fails the plan rather than surfacing after a
full matrix has been compiled. The cap itself is checked immediately after the
build, because a binary's size is not knowable before one exists; every target
is checked before anything is reported, so one run names all five. A binary
that reaches 90% of its budget is reported as a warning, since a cap that is
only ever discussed on the day it breaks tends to break on the day of a
release.

### `letsgo yank <tag>`

Go module releases cannot be unpublished — the proxy is immutable by design,
which is correct and also means a bad release is permanent. What Go *does*
provide is the `retract` directive, supported since Go 1.16 and almost never
used, because nothing automates it.

`letsgo yank v1.2.3` performs the full retraction:

1. Marks the GitHub release as a prerelease and prepends a notice to its body.
2. Generates the `retract` block in `go.mod`, with the rationale comment that
   `go list -retracted` surfaces to users.
3. Opens the commit or PR that adds it, since a retraction only takes effect
   once it is itself tagged.
4. Reverts the Homebrew formula to the previous version if a tap is configured.

Step 3 is the part people get wrong: a retraction must be published in a
*later* version to be visible. Encoding that sequencing is exactly the kind of
Go-specific knowledge a Go-only tool should own.

### Module proxy warm-up

Immediately after a release, issue a single GET to
`https://proxy.golang.org/<escaped-module>/@v/<version>.info` so the module is
cached before anyone runs `go install`. Without it there is a window where a
freshly tagged release is not yet installable, which reads to users as a broken
release. The module path requires case-escaping (uppercase letters become
`!`-prefixed lowercase). One HTTP request, one papercut permanently gone.

---

## 15. Migration

Moving a repository off GoReleaser is mostly deletion. Most of a
`.goreleaser.yaml` describes behaviour letsgo infers, so a ninety-line config
becomes nothing at all or a `letsgo.mod` of three or four lines.

There is deliberately no `letsgo migrate` command. A converter for a config
that mostly says "use the defaults" earns little, and the parts that would need
one are the parts letsgo does not do — where the answer is a decision about
which tool to use for that repository, not a translation. Building it would
also have cost the project its only dependency, a YAML parser, to read files
that are about to be deleted.

[MIGRATING.md](MIGRATING.md) carries the mapping, a worked example, and the
behaviour changes to expect on the first release: a renamed checksum file,
digests that no longer match because the archives are now reproducible, version
metadata taken from the commit rather than the clock, and a wrong `-X` path
that now fails instead of silently producing a binary reporting `dev`.

## 16. Architecture

```
cmd/letsgo/            entry point, subcommand routing (hand-rolled, no framework)
internal/config/       letsgo.mod parser, formatter, defaults, validation
internal/discover/     go.mod, git remote, main-package and symbol discovery
internal/safeexec/     resolving external binaries without trusting PATH
internal/gobuild/      target matrix, toolchain resolution, build execution
internal/archive/      deterministic tar/gzip/zip writers
internal/build/        compile, package, smoke, source archive, cache, SHA256SUMS
internal/bytesize/     one interpretation of "15MB", shared by config and reports
internal/gate/         apidiff and govulncheck, invoked as external tools
internal/semver/       version ordering and comparison
internal/bump/         next version from the API delta and the commits
internal/changelog/    conventional-commit and API-derived changelog
internal/manifest/     letsgo.json schema, read and write
internal/install/      the self-verifying install.sh generator
internal/publish/      GitHub releases, asset upload, resume logic, proxy warm-up
internal/release/      plan to a complete set of publishable files on disk
internal/verify/       rebuild-and-compare
internal/diff/         manifest-to-manifest release diffing
internal/plan/         orchestration, the release gate, and the plan report
internal/repro/        the cross-machine determinism suite, driving the real pipeline
```

Still to come: `internal/brew/` (formula generation and tap push),
`internal/oci/` (deterministic image layers and the registry client), and
`internal/yank/` (the retraction workflow).

### Dependency budget: zero direct dependencies

The GitHub API is accessed with `net/http` directly rather than through an SDK;
we use roughly eight endpoints. Config needs no dependency. Archives,
compression, and hashing are stdlib. `apidiff` and `govulncheck` are invoked as
external binaries (§9), keeping them out of the module graph.

The budget was ≤3, kept for a YAML parser that `migrate` would have needed.
Writing the migration as documentation instead spends none of it, so `go.mod`
has no require block at all.

A small dependency tree is not asceticism. This tool signs and publishes your
releases, so its attack surface is part of its threat model, and a fast
`go install` is part of its adoption story.

---

## 17. Milestones

MVP is M0 through M3.

**M0 — Foundation**
Skeleton and subcommand routing. `letsgo.mod` parser and formatter. Discovery
of module, git, main packages, and symbols. Target matrix. Deterministic
archive writers. `SHA256SUMS`. `letsgo build`. `plan --explain`. Major version
suffix check. **Both tiers of the determinism test in CI — the go/no-go gate
for the whole project.**

**M1 — The release path**
`letsgo plan` with the configuration gates and the smoke test. Changelog.
GitHub release creation and asset upload with resume. Manifest emission. Stable
source archive. Proxy warm-up. `--snapshot`. `letsgo release`.

**M2 — Verification and correctness gates**
`letsgo verify`. OIDC provenance attestation. Build cache. The `govulncheck`
and `apidiff` gates, plus the API-derived changelog section that falls out of
the latter.

**M3 — Adoption and afterlife**
A migration guide (MIGRATING.md) rather than a converter. `letsgo diff`, size
budgets, a generated `install.sh` that carries its own digests, and Homebrew
tap generation pointed at our own reproducible archives.

*MVP complete. letsgo can now replace GoReleaser across the repos it targets,
and does several things GoReleaser does not.*

**M4 — Container images**
Multi-arch OCI images built from the binaries M0 produced, on `scratch`, with a
deterministic layer and no daemon: the layer is the same tar writer as §7, the
config is JSON, and the push is the registry API. The image digest is recorded
in the manifest, so `letsgo verify` covers the image for the same reason it
covers an archive. This reverses a non-goal (§18) and is the one place it is
worth reversing, because "reproducible everywhere except the container" is not
a claim worth making.

**M5 — Breadth, only if needed**
`letsgo yank`. SBOM. An embeddable `selfupdate` package that consumes the
manifest, so any binary released with letsgo gets verified self-update for
free.

GitLab, Gitea and a reusable GitHub Action are out of scope here. The forge
interface (§16) exists so a second forge is an addition rather than a
refactor, and the Action is packaging rather than tooling — it belongs in its
own repository, where it can version independently of the binary it runs.

---

## 18. Non-goals

- ~~**Docker builds.**~~ Reversed in M4. The original reasoning was that
  `docker buildx` is better at building images, which is still true — and
  irrelevant, because letsgo does not need to build one. The binaries already
  exist; an image around a static binary on `scratch` is a tar layer and a JSON
  config, both of which §7's writer already produces deterministically. Wrapping
  buildx would have been wrong; reaching the registry API directly is a hundred
  lines, needs no daemon, and keeps the reproducibility claim whole.
- **deb, rpm, snap, scoop, AUR, krew, nix.** Not until a repo actually needs one.
- **Announcements** (Slack, Discord, Mastodon, email). Not a release tool's job.
- **A plugin system.** The moment plugins exist, the config surface reopens.
- **Non-Go languages.** The entire advantage comes from assuming Go.
- **Monorepos.** Deferred until there is one to test against.

Each of these — the reversed one aside — is a place where GoReleaser is the
better tool. Saying so is part of the design, and so is saying when the
reasoning stops holding.

---

## 19. Open decisions

1. **Config format.** `letsgo.mod` in `go.mod` syntax is the recommendation in
   §5, with alternatives recorded. Reversible now, expensive later.
2. **Snapshot version scheme.** `<last-tag>-next+<short-sha>` is proposed; it
   must sort correctly under semver against both the previous and the next real
   tag.
3. **Whether `letsgo build` requires a tag**, or defaults to snapshot semantics
   when `HEAD` is untagged. Leaning toward the latter.
4. **Whether the `apidiff` gate is default-on for libraries.** It is the most
   opinionated check in §9 and the most likely to annoy. Default-on with
   `--allow-breaking` is proposed, but default-warn is defensible for a first
   release while we learn its false-positive rate.
5. ~~**M3 scope.**~~ Settled and complete. `migrate` was dropped in favour of a
   guide, which also returns the dependency budget to zero; `diff`, size
   budgets, `install.sh` and Homebrew tap generation are built.
