# ADR-0001: Monorepo scope lives in core, not in a hook plugin

- Status: proposed
- Date: 2026-09-24
- Issue: [#24](https://github.com/danielriddell21/letsgo/issues/24)
- HLD: [hld/monorepo.md](../hld/monorepo.md)

## Context

A nested module (`services/api`) is released from prefixed tags
(`services/api/v1.2.0`). letsgo extends itself through hooks: a closed set of
questions, whose answers are recorded in the manifest so that `verify` never
runs a plugin. The question here is whether "which tag and directory is this
release" can be one of those hooks.

## Decision

The scope `(Dir, Prefix)` is resolved in core. A hookless companion,
`letsgo-mono`, handles orchestration across modules: list, changed, matrix,
tag and check.

## Consequences

- `verify` finds the manifest by tag (`internal/verify/verify.go:163`), so the
  tag must be known before any recorded answer can be read. Resolving it in
  core satisfies that.
- `selfupdate` and `install.sh` get prefix support with no plugin installed.
- Core changes in about 15 places (see the HLD table). For an unscoped
  repository every one of them is a no-op.
- `letsgo-mono` can never change released bytes.

## Alternatives considered

- **A `scope` hook.** Rejected: verify can't replay it, consumers outside a
  release need it, and it isn't a question at all, because Go already
  answers it.
- **Config inheritance** (a root `letsgo.mod` that nested modules extend).
  Rejected: an edit at the root would silently change every module's release.
