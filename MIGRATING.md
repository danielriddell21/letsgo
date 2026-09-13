# Migrating from GoReleaser

Most of a `.goreleaser.yaml` describes things letsgo infers, so migration is
mainly deletion. A ninety-line config usually becomes nothing at all, or a
`letsgo.mod` of three or four lines.

There is no `letsgo migrate` command. Translating a config that mostly says
"use the defaults" is not worth a tool, and the parts that would need one are
the parts letsgo deliberately does not do — where the right answer is a
decision, not a conversion.

---

## What to delete

Everything in this table is already the behaviour. Deleting it changes nothing.

| `.goreleaser.yaml` | letsgo |
|---|---|
| `builds.env: [CGO_ENABLED=0]` | default |
| `builds.goos` / `builds.goarch` | default matrix, see below |
| `builds.ldflags` with `-X main.version` etc. | injected automatically, and verified |
| `builds.flags: [-trimpath]` | always on, not optional |
| `archives.format: tar.gz` | default |
| `archives.format_overrides` → zip on Windows | default |
| `archives.name_template` | `<project>_<version>_<os>_<arch>` by default |
| `archives.files` with README and LICENSE | included when present |
| `checksum` | always produced |
| `snapshot.name_template` | `letsgo release --snapshot` |
| `changelog.filters.exclude` for `docs:`, `test:` | maintenance types are omitted and counted |
| `release.draft: false` | default |
| `project_name` | the module path's last element |

## What to keep

| `.goreleaser.yaml` | `letsgo.mod` |
|---|---|
| a non-default `goos`/`goarch` list | `build linux/amd64` … |
| extra `archives.files` | `archive NOTES.md` |
| `brews.tap` | `brew owner/tap` (the `homebrew-` prefix is added for you) |
| `dockers` / `docker_manifests` | `image` (see below) |
| `release.draft: true` | `release draft=true` |
| `project_name` differing from the module | `project name` |

## What does not migrate

These are deliberate non-goals. Each is a decision to make rather than a
setting to translate.

| `.goreleaser.yaml` | what to do instead |
|---|---|
| `dockers`, `docker_manifests` | build images with `docker buildx`; it is better at this and letsgo will not wrap it |
| `nfpms` (deb, rpm, apk) | keep GoReleaser for that repo, or publish the archives only |
| `snapcrafts`, `scoops`, `aurs`, `nix` | as above |
| `announce` | a separate CI step |
| `before.hooks: [go mod tidy]` | run it in CI before letsgo, not during a release: it can rewrite `go.sum` midway and change what you are releasing |
| `signs` with a cosign key | provenance is attested through the forge's own identity, so there is no key to manage |

Running both tools in different repositories is expected. letsgo is narrower on
purpose.

---

## A worked example

Before:

```yaml
project_name: mytool
before:
  hooks: [go mod tidy]
builds:
  - env: [CGO_ENABLED=0]
    goos: [linux, darwin, windows]
    goarch: [amd64, arm64]
    ldflags:
      - -s -w -X main.version={{.Version}} -X main.commit={{.Commit}} -X main.date={{.Date}}
archives:
  - format: tar.gz
    format_overrides:
      - goos: windows
        format: zip
    files: [README.md, LICENSE]
checksum:
  name_template: checksums.txt
changelog:
  filters:
    exclude: ['^docs:', '^test:', '^chore:']
release:
  draft: false
brews:
  - tap:
      owner: you
      name: homebrew-tap
```

After, in full:

```
brew you/homebrew-tap
```

Everything else was a default. If you do not publish a tap, delete the file.

---

## Differences to expect on the first release

These are behaviour changes, not configuration. Knowing them beforehand is the
difference between a surprise and a decision.

**Your checksums file is renamed.** GoReleaser's default is `checksums.txt`;
letsgo publishes `SHA256SUMS`. Anything downstream that fetches the old name
needs updating.

**Digests will not match your previous release.** The archives are constructed
differently — sorted entries, zeroed ownership, normalised modes, timestamps
taken from the commit. That is what makes them reproducible, and it means the
bytes differ from what GoReleaser produced for the same source. Expect new
checksums, and re-pin anything that recorded the old ones.

**Three extra assets appear.** A source archive, because a forge's generated
tarball is not guaranteed to be byte-stable and anything that pins its checksum
inherits that risk; `letsgo.json`, the manifest that makes `letsgo verify`
possible; and `install.sh`, which carries each archive's digest inline so that
installing verifies against a number recorded by the build.

**Your tap gets one formula per command.** GoReleaser's `brews` block produces
a single formula. letsgo writes `Formula/<binary>.rb` for each command the
module builds, because a formula can only name one archive per platform. A
single-command repository sees no difference; a multi-command one gains a
formula per binary instead of an error.

**Container images are built, not templated.** GoReleaser's `dockers` block
runs `docker build` against a Dockerfile you supply, then `docker_manifests`
stitches the architectures together. letsgo has neither: `image` publishes a
multi-arch image assembled from the binaries it just built, on `scratch` or on
a base you name. If your Dockerfile only did `COPY binary /` and set an
entrypoint, you can delete it. If it installs packages, keep GoReleaser and
buildx for that repository.

**A tap needs a token that is not the workflow's.** GitHub's default Actions
token cannot write to another repository, GoReleaser or not. If your tap
updates worked before, you already have a PAT or an App token configured —
letsgo reads the same `GITHUB_TOKEN`. `letsgo plan --publish` reports the
problem before anything is built rather than after everything is uploaded.

**Version metadata comes from the commit, not the clock.** GoReleaser's
`{{.Date}}` is when the build ran. letsgo uses the commit timestamp, because a
release that embeds the current time cannot reproduce. Your `--version` output
will show a different date than it used to for the same commit.

**A wrong `-X` path now fails the release.** The linker accepts a flag naming a
symbol that does not exist, which is why a binary can report `dev` forever
without anyone noticing. letsgo checks the symbol before building and stops. If
this fires on your first run, it was already broken — the release just never
said so.

**The default matrix is five targets, not six.** linux and darwin on amd64 and
arm64, plus windows/amd64. GoReleaser's defaults would also produce
windows/arm64. Add `build windows/arm64` if you want it.

**A clean worktree is required.** A release must be reproducible from its
commit alone, and uncommitted changes are inputs no commit describes.

---

## The move, in order

1. `letsgo plan` — resolves everything and runs the gates. Nothing is built, so
   this is cheap to repeat while you work through whatever it reports.
2. `letsgo plan --explain` — shows where each inferred value came from. Worth
   reading once, to confirm the inference matches what your config used to say.
3. `letsgo release --snapshot` — builds everything and rehearses publication
   against a recorder. Compare `dist/` against what GoReleaser produced.
4. Delete `.goreleaser.yaml`, write `letsgo.mod` if anything is left.
5. `letsgo release` on a tagged commit.

After the first release, `letsgo verify <tag>` rebuilds it from source and
compares against what was published — which is the thing the migration buys you
and the reason the digests changed.
