# PBS: `letsgo doctor`

> Product-based specification: what the product must do, stated so it can be tested.
> [PRD](../prd/doctor.md) · [HLD](../hld/doctor.md) · Issue [#31](https://github.com/danielriddell21/letsgo/issues/31)

## Scope

A read-only, offline diagnosis of tools and repository state.

## Interfaces

| surface | specification |
| --- | --- |
| CLI | `letsgo doctor [--json]` |
| text output | groups `tools` and `repository`, one line each: `<✓\|!\|✗> <name> <detail>`, plus a hint on `!`/`✗` |
| JSON | `{schema: 1, checks: [{group, name, status: "ok"\|"warn"\|"fail", detail, hint}]}` |
| exit code | 1 if any check is `✗`, else 0 |

## Requirements

| ID | requirement | story |
| --- | --- | --- |
| DR-1 | Doctor MUST report go, git, govulncheck and apidiff: found or not, with path and version. | 1 |
| DR-2 | A missing go or git MUST be `✗`; a missing govulncheck or apidiff MUST be `!`, with the install command. | 1 |
| DR-3 | A local go version different from `go.mod`'s `go`/`toolchain` MUST be `!`. | 2 |
| DR-4 | A `letsgo.mod` parse or decode error MUST be `✗` with `file:line:col`. | 3 |
| DR-5 | Each plugin pin that is missing or has a digest mismatch MUST be `✗`. | 4 |
| DR-6 | A shallow clone, or no tags, MUST be `!` with a `fetch-depth: 0` hint. | 5 |
| DR-7 | A non-GitHub `origin` MUST be `!`, naming the skipped outputs. | 6 |
| DR-8 | A dirty worktree MUST be `!`. | 7 |
| DR-9 | Under `require vulncheck` (#26), a missing govulncheck MUST be `✗`. | 1 |
| DR-10 | Doctor MUST NOT make network calls or write files. | — |
| DR-11 | Doctor MUST reuse plan's resolution, so that `doctor` and `plan` never disagree about the tools and config. | — |

## Acceptance scenarios

1. **Given** no govulncheck, **when** doctor runs, **then** it prints `! govulncheck` with the install line and exits 0. (DR-2)
2. **Given** a typo in `letsgo.mod`, **when** doctor runs, **then** it prints `✗` with its position and exits 1. (DR-4)
3. **Given** a shallow CI checkout, **when** doctor runs, **then** it prints the `fetch-depth: 0` hint. (DR-6)
4. **Given** network access disabled, **when** doctor runs, **then** every check still completes. (DR-10)
