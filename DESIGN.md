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

- **Packaging breadth** — Docker/ko, nfpm (deb/rpm/apk), snap, scoop, AUR,
  krew, nix. We do none of it, ever.
- **Announcements** — Slack, Discord, Mastodon, email.
- **Multi-forge** — GitLab and Gitea work today; for us they are M4 at best.
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
letsgo diff <tag> <tag>     size, dependency, and API deltas between two releases
letsgo yank <tag>           retract a bad release, including the go.mod directive
letsgo migrate              read .goreleaser.yaml, emit the equivalent letsgo config
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

State lives in two places, and neither is authoritative alone:

- **Local:** `dist/.letsgo-state.json` records each completed step keyed by
  content digest. A re-run in the same working directory is near-instant.
- **Remote:** before uploading, list the release's existing assets and compare
  name plus digest, falling back to size where the API does not expose a
  digest. Matching assets are skipped; mismatched assets are deleted and
  re-uploaded.

Because artifacts are reproducible, "already uploaded and correct" is a question
with a definite answer. That is what makes resume safe rather than hopeful.

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
Generated Homebrew formulas point at it rather than at the GitHub-generated
one. The cost is one extra asset; the benefit is that a checksum we publish is
a checksum we control.

---

## 14. After the release

### `letsgo diff <tag> <tag>`

Answers "what actually changed between these two releases" in terms of
artifacts rather than commit messages:

```
$ letsgo diff v1.2.3 v1.3.0
  binary size
    linux/amd64    12.4 MB -> 16.8 MB   (+4.4 MB, +35%)   ⚠ over budget
    darwin/arm64   12.1 MB -> 16.5 MB   (+4.4 MB, +36%)   ⚠ over budget
  dependencies
    + github.com/some/large-dep v1.4.0
    ~ golang.org/x/net v0.21.0 -> v0.23.0
  api
    + func WithTimeout(time.Duration) Option
  toolchain
    go1.23.4 -> go1.24.7
```

Size and dependency deltas come straight from the two manifests with no
rebuild, which makes this feature nearly free once §8 exists. Binary size
regressions are a real and under-tooled problem in Go, and a single added
dependency is usually the cause — putting both on the same screen makes the
culprit obvious.

**Size budgets.** Optional config (`budget linux/amd64 15MB`) turns this into a
§9 gate, failing a release that blows the budget rather than reporting it
afterwards.

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

`letsgo migrate` reads an existing `.goreleaser.yaml` and emits the equivalent
`letsgo.mod`. For a typical personal repo the output is under ten lines,
because most of a GoReleaser config restates defaults we already infer.

Anything mapping to a non-goal (Docker, snap, scoop, announcements) is reported
explicitly as unmigrated with a one-line explanation, never silently dropped.
The tool must never let you believe a release is equivalent when it is not.

---

## 16. Architecture

```
cmd/letsgo/            entry point, subcommand routing (hand-rolled, no framework)
internal/config/       letsgo.mod parser, formatter, defaults, validation
internal/discover/     go.mod, git remote, main-package and symbol discovery
internal/gobuild/      target matrix, build execution, build cache
internal/archive/      deterministic tar/gzip/zip writers
internal/checksum/     SHA256SUMS
internal/gate/         the release gate: semver, apidiff, govulncheck, smoke, budgets
internal/changelog/    PR, conventional-commit, and API-derived changelog
internal/manifest/     letsgo.json schema, read and write
internal/publish/      GitHub releases, asset upload, resume logic, proxy warm-up
internal/brew/         Homebrew formula generation and tap push
internal/verify/       rebuild-and-compare
internal/diff/         manifest-to-manifest release diffing
internal/yank/         retraction workflow
internal/plan/         orchestration and the plan report
internal/migrate/      .goreleaser.yaml importer
```

### Dependency budget: ≤3 direct dependencies

The GitHub API is accessed with `net/http` directly rather than through an SDK;
we use roughly eight endpoints. Config needs no dependency. Archives,
compression, and hashing are stdlib. `apidiff` and `govulncheck` are invoked as
external binaries (§9), keeping them out of the module graph. The one likely
dependency is a YAML parser used *only* by `migrate`, behind a build tag if
feasible.

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
`letsgo migrate`. Homebrew tap generation pointed at our own source archive.
Generated `install.sh`. `letsgo diff` and size budgets.

*MVP complete. letsgo can now replace GoReleaser across the repos it targets,
and does several things GoReleaser does not.*

**M4 — Breadth, only if needed**
`letsgo yank`. SBOM. GitLab and Gitea. A reusable GitHub Action. An embeddable
`selfupdate` package that consumes the manifest, so any binary released with
letsgo gets verified self-update for free.

---

## 18. Non-goals

- **Docker builds.** `docker buildx` is better at this. We will not wrap it.
- **snap, scoop, AUR, krew, nix.** Not until a repo actually needs one.
- **Announcements** (Slack, Discord, Mastodon, email). Not a release tool's job.
- **A plugin system.** The moment plugins exist, the config surface reopens.
- **Non-Go languages.** The entire advantage comes from assuming Go.
- **Monorepos.** Deferred until there is one to test against.

Each of these is a place where GoReleaser is the better tool. Saying so is part
of the design.

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
5. **M3 scope.** MVP now carries `diff`, `migrate`, Homebrew, and `verify`
   between them. If the timeline matters more than completeness, `diff` is the
   cheapest to defer, since the manifest makes it easy to add later.
