# HLD: `letsgo doctor`

Status: proposal. Nothing here is implemented.

| | |
|---|---|
| Issue | [#31](https://github.com/danielriddell21/letsgo/issues/31) |
| Epic | [#40](https://github.com/danielriddell21/letsgo/issues/40) |
| PRD | [prd/doctor.md](../prd/doctor.md) |
| PBS | [pbs/doctor.md](../pbs/doctor.md) |
| ADRs | none |

## Problem

Setup problems show up mid-release, or as a plan `Skip` that is easy to miss:
govulncheck isn't installed, the clone is shallow, a plugin pin doesn't
match, the wrong `go` is on PATH. Each one costs a CI run to discover.

## Proposal

`letsgo doctor` is **read-only**, makes **no network calls**, and prints one
line per check with a fix hint. It exits non-zero on any `✗`.

```
  tools
  ✓ go         go1.26.2 (/usr/local/go/bin/go)
  ! go.mod     wants go1.26.2; GOTOOLCHAIN will switch
  ✓ git        /usr/bin/git
  ! govulncheck not found — go install golang.org/x/vuln/cmd/govulncheck@latest
  ✓ apidiff    v0.0.0-2026…

  repository
  ✓ letsgo.mod parses
  ✗ plugin     letsgo-multi v0.3.0: digest mismatch (installed sha256:ab12…)
  ! history    shallow clone — use fetch-depth: 0
  ✓ remote     github.com/you/gambit
  ✓ worktree   clean
```

### Tools

| check | source | result |
| --- | --- | --- |
| `go` resolved (GOROOT, then PATH, or `LETSGO_GO`) | `gobuild.Toolchain` | ✗ not found |
| `go` version vs the `go`/`toolchain` line in `go.mod` | `gobuild.Version` | ! differs: a local build won't match CI |
| `git` found (system directories only) | `safeexec` | ✗ not found |
| `govulncheck`, `apidiff` found, with versions | `gate/tool.go` | ! missing, with the install line (the gate Skips) |

### Repository

| check | result |
| --- | --- |
| `letsgo.mod` parses and decodes | ✗ with `file:line:col` |
| each plugin pin is found and its digest matches | ✗ mismatch (the `plugin list` logic) |
| history is not shallow and tags are present | ! shallow, with a `fetch-depth: 0` hint |
| `origin` remote is GitHub | ! install.sh and brew are skipped on other forges |
| worktree is clean | ! `release` would refuse |

## Implementation

- Reuse what `plan` already resolves. Don't write a second implementation:
  `doctor` is plan's discovery phase with a checks-only report, plus
  tool-version lines.
- A `--json` mode per #28, with `{schema, checks: [{group, name, status,
  detail, hint}]}`.

## With other proposals

- **#27:** also reports the global `config.mod` path and the plugin store.
- **#28:** the editor panel can show doctor's output.
- **#26:** a `require vulncheck` repository escalates a missing govulncheck
  from `!` to `✗`.

## Out of scope

- Token and scope checks, and workflow linting. Both need the network or
  guesswork; they could come later.

## Open questions

- A separate verb, or `plan --doctor`? Proposed: a separate verb, since the
  name is well known.

## Delivery

Phases, in order. Each is a vertical slice: a thin path through every layer, verified end to end. The behaviour each phase must meet is specified in the [PBS](../pbs/doctor.md).

1. Tracer bullet: tool checks (user stories 1, 2)
2. Repository checks (user stories 3, 4, 5, 6, 7)
3. `--json` (user stories 8)
