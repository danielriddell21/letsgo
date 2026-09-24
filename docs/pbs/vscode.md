# PBS: Editor support

> Product-based specification: what the product must do, stated so it can be tested.
> [PRD](../prd/vscode.md) · [HLD](../hld/vscode.md) · Issue [#28](https://github.com/danielriddell21/letsgo/issues/28) · ADR [0008](../adr/0008-editor-binary-is-the-brain.md)

## Scope

Core machine output (`--json`, check positions, `fmt -`), the `letsgo lsp`
language server, and the `letsgo-vscode` extension.

## Interfaces

| surface | specification |
| --- | --- |
| `--json` | on `plan`, `plugin list`, `tag`, `verify` and `features`; top level `{"schema": 1, ...}` |
| `Check` | optional `pos: {file, line, col}` |
| `fmt -` | reads stdin, writes the formatted result to stdout, exit 1 on a parse error |
| `letsgo lsp [--restricted]` | stdio JSON-RPC: `initialize`, `textDocument/didOpen\|didChange\|didSave`, `publishDiagnostics`, `formatting`, `completion`, `hover`, `documentSymbol`, `codeAction` |
| extension | settings `letsgo.path`; commands Plan, Plan with analysis, Build snapshot, Rehearse release, Tag next version, Verify release, Diff releases, Install pinned plugins, Update; task type `letsgo` |

## Requirements

| ID | requirement | story |
| --- | --- | --- |
| ED-1 | Parse and decode errors MUST be published as diagnostics on every change. | 1 |
| ED-2 | Checks derived from config MUST carry a position, and MUST appear as diagnostics on save. | 2 |
| ED-3 | Completion MUST offer directive names, build targets, hooks, feature names and first-party plugins. | 3 |
| ED-4 | Hover over a directive MUST show its documentation. | 4 |
| ED-5 | Formatting MUST equal `letsgo fmt` output byte for byte. | 5 |
| ED-6 | The "update pin" code action MUST rewrite the version and digest of a pin line. | 6 |
| ED-7 | The panel MUST show, per module, the resolved values, gates, artifacts, features and plugins from `plan --json`. | 7 |
| ED-8 | The status bar MUST show the version and the number of gate failures. | 8 |
| ED-9 | "Tag next version" MUST show the proposed level and reason, then create the tag only on confirmation. | 9 |
| ED-10 | The manifest viewer MUST offer Verify, which runs `letsgo verify <tag>`. | 10 |
| ED-11 | In an untrusted workspace the extension MUST NOT run `plan`, tasks or plugins, and MUST start `lsp --restricted`. | 11 |
| ED-12 | `lsp` MUST work with any LSP client (no VS Code-specific extensions required). | 12 |
| ED-13 | Every `--json` output MUST carry `schema`, and field removals MUST bump it. | 13 |
| ED-14 | The extension MUST NOT publish releases or store tokens. | — |

## Errors and edge cases

- letsgo not found: one notification offering `go install`. No repeated
  errors.
- A letsgo version older than the one with `--json`/`lsp`: the extension
  lists which features are unavailable.
- A plan timeout on save: the diagnostics from parsing stay; the plan
  diagnostics are marked stale.

## Acceptance scenarios

1. **Given** `budget windows/arm64 10MB` where that target isn't built, **when** the file is saved, **then** a diagnostic appears on that line. (ED-2)
2. **Given** an untrusted workspace, **when** it's opened, **then** no `letsgo plan` process starts. (ED-11)
3. **Given** Neovim's built-in LSP client, **when** `letsgo.mod` is opened, **then** diagnostics and completion work. (ED-12)
4. **Given** `plan --json`, **when** the output is validated, **then** it matches the documented schema 1. (ED-13)
