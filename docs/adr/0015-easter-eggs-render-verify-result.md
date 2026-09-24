# ADR-0015: Easter eggs are renderers over `verify.Result`, never separate paths

- Status: proposed
- Date: 2026-09-24
- Issue: [#36](https://github.com/danielriddell21/letsgo/issues/36)
- HLD: [hld/receipt.md](../hld/receipt.md)

## Context

Hidden, playful outputs such as `verify --receipt` must never be able to say
something that plain `verify` wouldn't. An easter egg that reports ✓ where
verify reports ✗ would be an integrity bug disguised as a joke.

## Decision

- Every alternative output renders the same `verify.Result`: same checks,
  same exit code.
- Every row maps to a check. Skipped checks are shown, never hidden.
- A test such as `TestReceiptMatchesVerify` asserts that the total is ✓
  exactly when `Result.OK()` is true, across all fixtures.
- Hidden means "left out of `--help`", not "untested".

## Consequences

- Eggs are safe to add.
- The rule also applies to #28's `--json` and any future formats.

## Alternatives considered

- **Independent quick checks for fun output.** Rejected: that would be a
  second verify path.
