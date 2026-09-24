# ADR-0012: Audit results are append-only and outside the release's integrity set

- Status: proposed
- Date: 2026-09-24
- Issue: [#30](https://github.com/danielriddell21/letsgo/issues/30)
- HLD: [hld/audit.md](../hld/audit.md)

## Context

`letsgo audit` re-checks shipped releases against today's vulndb. The results
have to live somewhere consumers can see them, without changing what the
release *is*.

## Decision

- Results are appended to an `audit.json` beside the release: one dated entry
  per run, deduplicated when nothing changed.
- `audit.json` is **not** in `SHA256SUMS` or the manifest. `verify` treats it
  as a known extra asset and prints only its latest entry, for information.
- Audit never touches the release's own assets.
- Findings don't fail the run; only an audit that couldn't run fails.

## Consequences

- Consumers see history ("affected since 2026-09-24"), not just the latest
  status.
- A release's integrity result never depends on the date it was checked.
- Immutable releases can't take new assets. The proposed fallback is an
  `audit` branch with `<tag>.json`, written through the contents API.

## Alternatives considered

- **Failing the workflow on findings.** Rejected: findings are data, and red
  scheduled runs get ignored.
- **Editing the release body.** Rejected: it's lossy and races with humans.
