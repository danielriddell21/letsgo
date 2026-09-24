# HLD: letsgo for VS Code

Status: proposal. Nothing here is implemented.

| | |
|---|---|
| Issue | [#28](https://github.com/danielriddell21/letsgo/issues/28) |
| Epic | [#40](https://github.com/danielriddell21/letsgo/issues/40) |
| PRD | [prd/vscode.md](../prd/vscode.md) |
| PBS | [pbs/vscode.md](../pbs/vscode.md) |
| ADRs | [ADR-0008](../adr/0008-editor-binary-is-the-brain.md) |

## What the CLI offers an editor today

| need | today |
| --- | --- |
| diagnostics for `letsgo.mod` | `config.Parse`/`Decode` return `file:line:col: msg` (`SyntaxError`, `errAt`) — good |
| formatting | `letsgo fmt [file]` rewrites in place; no stdin/stdout mode |
| the plan | `plan [--explain]` prints for people; **no machine-readable output anywhere in the CLI** |
| positions on plan problems | `Check` has name, status, detail — **no position**, even for config-derived ones (`budget names windows/arm64, which is not a target…`) |
| directive docs | the `known` usage strings in `config/decode.go`; nothing longer |
| plugin state | `plugin list` prints text |

letsgo has **zero dependencies** (`go.mod` has no `require`), and hand-writes
its parser rather than importing one. The extension design keeps that true.

## Principle: the binary is the brain

Every lesson from #25 applies: the moment the extension carries its own copy of
the directive table, the hook list or the feature catalogue (#26), it drifts.
So the extension is a thin TypeScript client, and **everything it knows comes
from the installed `letsgo`**:

- `letsgo lsp` — a language server over stdio, built into the CLI, reusing
  `internal/config` directly;
- `--json` on the commands the extension displays.

A bonus of that shape: the language server works in Neovim, Helix, Zed and
anything else that speaks LSP, for free. The VS Code extension is just the
first client.

## Core prerequisites (letsgo repo)

1. **`--json`** on `plan`, `plugin list`, `tag` (as a dry-run proposal),
   `verify`, and `features` (#26). One schema per command, versioned
   (`"schema": 1`) like `letsgo.json`, documented as a compatibility promise.
2. **Positions on checks.** `Check` gains an optional `Pos` (file, line, col).
   Every check raised from a config value sets it, and
   `TestConfigChecksCarryPositions` holds that in step. This is what turns
   "the budget is wrong" into a squiggle under the budget line.
3. **`letsgo fmt -`** — stdin to stdout, for format-on-save without writing the
   file behind the editor's back.
4. **`letsgo lsp`** — see below. Hand-rolled JSON-RPC over stdio, implementing
   only the subset used; that's a few hundred lines, in keeping with the
   parser.
5. **Docs on the directive table.** `known` grows from a usage string to
   `{usage, doc}`, so hover and completion show a paragraph, and the wiki can be
   generated from the same text.

## `letsgo lsp`

Serves `letsgo.mod`, the global `config.mod` and `.letsgo/*.mod` (#27).

| LSP feature | from |
| --- | --- |
| diagnostics | `Parse` + `Decode` on every change; on save, a fast `plan` (no `--analyse`, no forge) for position-carrying checks |
| formatting | `File.Format` |
| completion | directive names (`known`); `build` targets (`go tool dist list`, cached); hooks (`plugin.Hooks`); `disable`/`require` names (#26); `release` keys; for `plugin` lines, first-party names from the catalogue (#25) |
| hover | directive docs; on a `plugin` line, installed/pinned/mismatch with paths; on a target, whether it is built |
| code actions | did-you-mean fixes (the existing `nearestKeyword`); **update pin** (runs `plugin install`, rewrites version + digest); **install pinned plugins**; move `letsgo-env.mod` → `.letsgo/env.mod` (#27); a global-only directive in `letsgo.mod` or vice versa → move it |
| document symbols | directives and blocks, for the outline |
| semantic tokens | not needed — a TextMate grammar covers the syntax, which is go.mod's |

`.letsgo/<plugin>.mod` files get syntax and formatting only: their directives
belong to the plugin. If plugins ever want more, a `describe` hook returning
their own `{directive, usage, doc}` table is the extension point — core would
serve it without knowing it. Deferred until a plugin asks.

## The extension (`letsgo-vscode`)

A new repository, TypeScript, published to the VS Code Marketplace and Open
VSX. (It cannot be released with letsgo — letsgo is Go-only, and that is the
right call; it uses `vsce` in CI.)

### Finding letsgo

Setting `letsgo.path`, then `$GOBIN`, `$GOPATH/bin`, `~/go/bin`, then `PATH` —
the same search `gate/tool.go` uses. Missing → one notification offering
`go install github.com/danielriddell21/letsgo/cmd/letsgo@latest` in a
terminal. The extension checks `letsgo version` against the minimum that has
`lsp` and `--json`, and says which features are unavailable rather than
failing obscurely.

### Views

**letsgo** panel (Explorer or its own activity-bar icon), one tree per module —
several in a monorepo (#24), found by `go.mod`s with a `letsgo.mod` or main
packages:

```
services/api  1.2.0 · a1b2c3d
├─ resolved            (plan --explain)
│   ├─ version  1.2.0      git tag services/api/v1.2.0
│   └─ targets  5          letsgo.mod:3
├─ gates
│   ├─ ✓ tag
│   ├─ ✗ budget   linux/amd64 is 18MB, over 15MB    → letsgo.mod:7
│   └─ · vulnerabilities   not run (Run analysis)
├─ artifacts           (group → targets → binaries)
├─ features            (#26: departures from defaults)
└─ plugins             (pins, with install state)
```

Refreshes on save of `letsgo.mod`/`go.mod`/`.letsgo/*`, and on HEAD or tag
change (a `FileSystemWatcher` on `.git/HEAD` and `.git/refs/tags`). The slow
gates (`--analyse`: govulncheck, apidiff) run only on demand, from the tree.

**Status bar:** `letsgo 1.2.0 ✓`, `letsgo snapshot`, or `letsgo ✗ 2` — click
opens the panel.

**`letsgo.json` viewer:** a custom editor for manifests (local `dist/` or
downloaded): artifacts with digests, gates, features, plugin records, and a
**Verify** CodeLens that runs `letsgo verify <tag>`.

### Commands and tasks

| command | runs | notes |
| --- | --- | --- |
| Plan / Plan with analysis | `plan --json [--analyse]` | feeds the panel |
| Build snapshot | `build --snapshot` | task, output in terminal |
| Rehearse release | `release --snapshot` | the recorder's "would publish" output |
| Tag next version | `tag --json` → quick pick (major/minor/patch preselected with the reason) → `tag --yes` | the one write the extension makes routinely |
| Verify release | `verify [tag]` | quick pick of releases |
| Diff releases | `diff <from> [to]` | output in a read-only document |
| Install pinned plugins | `plugin install` (#27) | |
| Update letsgo | `update` | |

A `letsgo` task type (`tasks.json`) with a problem matcher for
`letsgo.mod:L:C: message`, so CI-like runs surface in Problems.

**Publishing from the editor is deliberately absent.** letsgo's model is tag
locally, release in CI (letsgo-action), where the token is minted per run.
The extension offers *Tag* and *Rehearse*, and after tagging, a button to
**push the tag** — which is what triggers the real release. A `letsgo.allowPublish`
setting could unlock a real `release` later, behind a modal; not in v1, and no
token storage in v1 either (consistent with #27's "no tokens in files").

### Workspace Trust

`plan` executes things the repository chooses: pinned plugins (a pin's
`Command` may be a **path inside the repository**, `plugin.go:60`), git, and
the go command. So:

- **Untrusted workspace:** the language server runs in a parse-only mode
  (`letsgo lsp --restricted`: `Parse`/`Decode`/format/completion, no plan, no
  plugin lookup). No panel data, no tasks.
- **Trusted:** everything.

`capabilities.untrustedWorkspaces: "limited"` in the manifest, with the
restricted flag passed by the client. Worth a core check too: a plugin
`Command` that is a relative path could be a plan **Warn** regardless of the
editor.

## With the other proposals

- **#24 monorepo:** one tree per module; tags shown with their prefix; *Tag
  next version* runs in the module's directory.
- **#25 plugin-aware core:** completion and hover for first-party plugins come
  from the catalogue via the server; **update pin** is the natural code action.
- **#26 features:** `disable`/`require` completion and a *features* node;
  hover explains what disabling costs, from the catalogue's summary.
- **#27 config dirs:** the server serves the global `config.mod` with its own
  table, and flags directives in the wrong file with a move action.

## Alternatives

- **Pure TypeScript extension** parsing `letsgo.mod` itself: faster to start,
  but a second parser and a second directive table — the #25 drift problem,
  built in from day one. Rejected.
- **CLI + `--json` only, no LSP:** the panel works, but diagnostics would come
  from re-running a process per keystroke, and other editors get nothing.
  `--json` is still needed for the panel, so it is phase 1 either way.
- **Put the extension in the letsgo repo:** one repo, but a Node toolchain in
  a zero-dependency Go repository, and CI for a second ecosystem. Rejected.

## Delivery

Phases, in order. Each is a vertical slice: a thin path through every layer, verified end to end. The behaviour each phase must meet is specified in the [PBS](../pbs/vscode.md).

1. Core machine output (user stories 2, 5, 13)
2. Tracer bullet: extension v0 (user stories 7, 8, 11)
3. `letsgo lsp` (user stories 1, 3, 4, 12)
4. Extension v1 (user stories 6, 9, 10)

## Open questions

- Should the extension prefer a repository-pinned letsgo version (e.g. a
  `letsgo` line in `letsgo.mod`, or what letsgo-action's workflow pins) over
  whatever is on PATH? The version shapes the archive layout, so a mismatch
  shows a plan CI won't produce.
- Own activity-bar icon, or a section in the Explorer? Proposed: Explorer
  section, promoted to its own container only if the monorepo view needs room.
- Is a separate `letsgo-vscode` repo in scope for this GitHub account now, or
  should phases 1 and 3 land first and the extension wait?
