# ADR-0008: Editor support: the binary is the brain

- Status: proposed
- Date: 2026-09-24
- Issue: [#28](https://github.com/danielriddell21/letsgo/issues/28)
- HLD: [hld/vscode.md](../hld/vscode.md)

## Context

A VS Code extension needs the directive table, the hooks, the feature
catalogue and the plan. Any copy of those in TypeScript would drift, as
letsgo-plugins did (see [ADR-0003](0003-plugin-logic-in-core-thin-mains.md)).
letsgo has zero dependencies.

## Decision

- Everything the extension knows comes from the installed binary, through
  `letsgo lsp` and `--json`.
- `letsgo lsp` is a hand-rolled subset of JSON-RPC, reusing
  `internal/config`.
- The extension is a thin TypeScript client in a **separate** `letsgo-vscode`
  repository.
- In an untrusted workspace it runs `letsgo lsp --restricted` (parse only).
- There is no publishing in v1.

## Consequences

- Neovim, Helix and Zed get the same language server for free.
- `--json` (versioned) becomes a compatibility promise that letsgo-action and
  scripts can also use.
- Checks gain an optional `Pos`, so problems can be underlined in the editor.

## Alternatives considered

- **A pure TypeScript parser.** Rejected: drift from day one.
- **`--json` only, without an LSP.** Rejected: a process per keystroke, and
  nothing for other editors.
- **The extension inside the letsgo repository.** Rejected: it would add a
  Node toolchain to a zero-dependency Go repository.
