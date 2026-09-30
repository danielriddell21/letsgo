# PRD: Editor support (`--json`, `letsgo lsp`, VS Code)

| | |
|---|---|
| Issue | [#28](https://github.com/danielriddell21/letsgo/issues/28) |
| Epic | [#40](https://github.com/danielriddell21/letsgo/issues/40) |
| HLD | [hld/vscode.md](../hld/vscode.md) |
| PBS | [pbs/vscode.md](../pbs/vscode.md) |
| ADRs | [0008](../adr/0008-editor-binary-is-the-brain.md) |

## Problem Statement

I edit `letsgo.mod` with no highlighting, completion or diagnostics, and I
learn about a bad budget line only when plan runs. Nothing in the CLI has
machine-readable output, so neither scripts nor an editor can show me the
plan, gates or plugin state.

## Solution

Core gains `--json` output, positions on checks, `fmt -`, and a built-in
language server (`letsgo lsp`). A thin VS Code extension in its own
repository uses them: diagnostics, completion, hover, code actions, a panel
per module, a status bar, tasks, and a manifest viewer. Other editors get the
same LSP.

## User Stories

1. As a maintainer, I want syntax errors in `letsgo.mod` shown as I type, so that I fix them before running anything.
2. As a maintainer, I want config-derived plan problems shown on the offending line, so that I know what to change.
3. As a maintainer, I want completion for directives, targets, hooks and feature names, so that I don't look them up.
4. As a maintainer, I want hover docs for each directive, so that I learn the config in place.
5. As a maintainer, I want format-on-save, so that `letsgo.mod` stays canonical.
6. As a maintainer, I want an "update pin" code action, so that bumping a plugin is one click.
7. As a maintainer, I want a panel showing the resolved plan, gates, artifacts, features and plugins per module, so that I see the release before tagging.
8. As a maintainer, I want a status bar showing version and gate state, so that problems are visible at a glance.
9. As a maintainer, I want a "Tag next version" command with the proposed bump and its reason, so that tagging is easy and correct.
10. As a user, I want to open a `letsgo.json` and verify it, so that checking a release is one click.
11. As a security-minded user, I want untrusted workspaces never to run plan, so that opening a repository can't execute its plugins.
12. As a Neovim, Helix or Zed user, I want the same language server, so that I'm not left out.
13. As a script author, I want `--json` with a versioned schema, so that I can build on letsgo's output.

## Implementation Decisions

- The binary is the brain: the extension carries no copy of the tables
  ([ADR-0008](../adr/0008-editor-binary-is-the-brain.md)).
- `--json` is versioned (`"schema": 1`) on plan, plugin list, tag, verify and
  features.
- `Check` gains an optional `Pos`.
- The directive table grows from `known` to `{usage, doc}`.
- `letsgo lsp` is hand-rolled JSON-RPC, and `--restricted` means parse only.
- The extension lives in a new `letsgo-vscode` repository, with no publishing
  and no token storage in v1.

## Testing Decisions

- `TestConfigChecksCarryPositions`.
- Golden JSON per command, with a schema version test.
- LSP request/response fixtures for each supported method.
- Extension tests against a stub binary, including a Workspace Trust test.

## Out of Scope

- Publishing from the editor.
- A plugin `describe` hook for `.letsgo/*.mod`.

## Further Notes

- Open questions: prefer a repository-pinned letsgo over the one on PATH?
  Explorer section or its own activity-bar container?
