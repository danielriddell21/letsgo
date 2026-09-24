# HLD: enabling and disabling features

Status: proposal. Nothing here is implemented.

| | |
|---|---|
| Issue | [#26](https://github.com/danielriddell21/letsgo/issues/26) |
| Epic | [#39](https://github.com/danielriddell21/letsgo/issues/39) |
| PRD | [prd/features.md](../prd/features.md) |
| PBS | [pbs/features.md](../pbs/features.md) |
| ADRs | [ADR-0005](../adr/0005-feature-catalogue-disable-require.md) |

A repository should be able to say, in `letsgo.mod`, which of letsgo's
optional behaviours it wants. Integrity has to stay something a repository
cannot turn off, and whatever a repository does turn off should be visible to
anyone consuming the release.

## Where things are today

Every feature, and how a repository changes it now:

| feature | kind | default | how it is changed today | where the choice lives |
| --- | --- | --- | --- | --- |
| reproducible build, source archive, `letsgo.json`, `SHA256SUMS` | integrity | on | cannot be | — |
| tag and module-path checks | integrity | on | cannot be (`--snapshot` skips the tag) | — |
| clean worktree | integrity | on | `--allow-dirty` (`plan` and `build` only) | CLI |
| vulnerability gate | gate | on, **Skip if `govulncheck` is missing** | `--allow-vulnerable` | CLI |
| API compatibility gate | gate | on | `--allow-breaking` | CLI |
| size budget | gate | off | `budget` directive | config |
| SBOM | output | on | cannot be | — |
| `install.sh` | output | on if GitHub and a tag | cannot be; skipped implicitly on other forges | code |
| changelog as release body | output | on | `--append-notes` changes how, not whether | CLI |
| proxy warm | publish | on | `--no-proxy-warm` | CLI |
| Homebrew formula | publish | off | `brew` directive; `yank --keep-tap` | config, CLI |
| container image | publish | off | `image` directive | config |
| draft / prerelease | publish | auto | `release draft= prerelease=` | config |

What the table shows:

1. **Turning something on is already consistent.** Writing a directive
   (`brew`, `image`, `budget`) is how a repository opts in. That should stay
   the only way, not a second switch next to it.
2. **Turning something off is not.** It's a CLI flag, or it isn't possible at
   all. A flag lives in a workflow file, so it isn't part of the repository's
   release definition, and `plan` run on a laptop can't see it.
3. **You can't make a gate stricter.** The vulnerability gate *Skips* when the
   tool is missing. That's the right default, but a repository that relies on
   it being checked can't say "if this didn't run, fail".
4. **The manifest records gates, not features.** `gates` in `letsgo.json` holds
   check statuses. There's nothing a consumer can read to tell "no SBOM
   because the repository opted out" apart from "no SBOM because this was
   published by an older letsgo".

## Proposal

### Two directives

```
// letsgo.mod
disable sbom proxy-warm
require vulncheck
```

- `disable <feature>...` turns off a feature that is on by default.
- `require <feature>...` makes a gate strict: a Skip becomes a Fail.

Both are list directives (they can repeat), and both check their names against
a closed set, just as unknown directives are rejected today. `disable image` is
an error that says "remove the `image` directive". Features that are off by
default are enabled by their own directive, never by a second switch.

### A single catalogue

```go
// internal/feature
type Kind int // Integrity, Gate, Output, Publish

type Feature struct {
	Name     string
	Kind     Kind
	Default  bool   // on without any config
	Enable   string // the directive that opts in, for off-by-default features
	Disable  bool   // may `disable` name it
	Require  bool   // may `require` name it
	Summary  string
}

var All = []Feature{ … }
```

This one table drives:

- **config validation**: `disable`/`require` names, with did-you-mean, using
  the existing `nearestKeyword`;
- **plan**: resolves `p.Features`, a set where each feature records whether it
  is on and where that came from, for `--explain`;
- **the report**: a `features` line listing only what differs from the
  defaults;
- **the manifest**: a new `features` field (see below);
- **`letsgo features`**: prints the catalogue with each feature's state in this
  repository, where it came from, and the directive that changes it;
- **a test**, `TestEveryFeatureIsConsulted`, in the same style as
  `TestEveryDirectiveIsHandled`, so a feature in the table that no code path
  checks is a failing test, not a switch that does nothing.

Code sites change from implicit conditions to `p.Features.On(feature.SBOM)`.

### What each feature allows

| feature | `disable` | `require` | notes |
| --- | --- | --- | --- |
| `reproducible`, `source`, `manifest`, `checksums` | **no** | — | they are what letsgo is. The error says so and points to GoReleaser, as the README does |
| `tag-check`, `module-path` | **no** | — | a wrong major version is the failure mode letsgo exists to catch |
| `vulncheck` | yes | yes | `disable` is for repositories that run govulncheck elsewhere; `require` turns a missing tool into a Fail |
| `api-gate` | yes | yes | `disable` suits command-only modules whose exported API is incidental |
| `sbom` | yes | — | |
| `install-script` | yes | yes | `require` makes the implicit "not GitHub, so no script" a Fail instead of a silent absence |
| `changelog` | yes | — | disabled: the release body is left as the forge or a person wrote it |
| `proxy-warm` | yes | — | the default for private modules, since the public proxy can't fetch them anyway |
| `brew`, `image`, `budget` | — (remove the directive) | — | enabled by their directive |

**Gate overrides stay on the CLI.** `--allow-vulnerable` and `--allow-breaking`
are decisions about one release, made by a person, and they're already
recorded as Warn in the manifest's `gates`. A permanent version in config
(`disable vulncheck`) is a different statement, and the manifest records it as
a different statement.

**Existing flags keep working.** `--no-proxy-warm` means `disable proxy-warm`
for one run. No generic `--disable` flag is added: a release's shape should be
readable from the repository.

### The manifest records it

```json
"features": {
  "disabled": ["sbom"],
  "required": ["vulncheck"]
}
```

Only departures from the defaults are recorded, so the field is empty for most
releases. With `gates`, a consumer can tell apart:

- the vulnerability gate passed;
- it was skipped because the tool was missing (`gates`: Skip, not required);
- the repository opted out (`features.disabled`).

`verify` reports the `features` line as it reports gates. It changes nothing
about what is rebuilt: every feature that can be disabled is an output or a
check, never an input to the bytes.

## With plugins and monorepos

- **Plugins stay the way to add behaviour.** `disable` removes core behaviour
  and never installs anything. With the plugin catalogue from #25,
  `letsgo features` can list first-party plugins under "available", so a
  repository sees everything it can turn on in one place.
- **A plugin can't disable a core feature.** Hooks answer questions; they don't
  switch features. Otherwise pinning `letsgo-cask` could silently turn off the
  SBOM.
- **Monorepos (#24).** Each module's `letsgo.mod` sets its own features. A
  library-only module might `disable install-script`. `letsgo-mono check`
  can flag modules whose gate settings differ from their siblings'.

## Alternatives

- **Per-feature directives** (`sbom off`, `changelog off`). This is closer to
  the existing `release draft=true`, but every feature would grow its own
  directive, and "what is off in this repository" would need reading the whole
  file. Rejected.
- **A `features ( … )` block with `on`/`off` lines.** Readable, but it gives
  off-by-default features a second way to be enabled, next to their directive.
  Rejected, for reason 1 above.
- **CLI only.** Today's approach. Rejected: `plan` on a laptop can't see what
  CI will do.

## Delivery

Phases, in order. Each is a vertical slice: a thin path through every layer, verified end to end. The behaviour each phase must meet is specified in the [PBS](../pbs/features.md).

1. Tracer bullet: catalogue and `letsgo features` (user stories 6)
2. `disable` (user stories 1, 3, 4, 5, 7, 9, 10)
3. `require` (user stories 2)
4. `verify` and the report (user stories 8)

## Open questions

- Should `require` exist for outputs beyond `install-script` (e.g. `require
  sbom` to guard against a future default change)? Proposed: gates and
  install-script only, until asked.
- Should `disable changelog` leave an existing body untouched, or write an
  empty one? Proposed: untouched, the same code path as `--append-notes`, with
  nothing appended.
- Is `api-gate` worth auto-skipping for modules with no importable packages,
  instead of making those repositories write `disable api-gate`?
