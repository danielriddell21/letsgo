# ADR-0016: A plan holds intent, not bytes; apply rebuilds and compares

- Status: proposed
- Date: 2026-09-27
- Issue: [#50](https://github.com/danielriddell21/letsgo/issues/50)
- HLD: [hld/plan-apply.md](../hld/plan-apply.md)

## Context

A saved plan could carry the built artifacts, so apply only uploads them. Or
it could carry only what should happen, including every predicted digest, so
apply rebuilds. letsgo's position everywhere else is that bytes are never
trusted, they are reproduced: `verify` rebuilds, and promote rebuilds and
compares ([ADR-0009](0009-promotion-rebuilds-at-final-tag.md)).

## Decision

- The plan file contains the resolved config digest, the tag and commit, the
  **predicted manifest** (every artifact digest), and the forge actions. It
  contains no artifact bytes.
- `apply` rebuilds, and refuses if any digest differs from the predicted
  manifest.
- The manifest records `plan: {sha256, created_at, letsgo_version}`, and the
  plan file is attached as `letsgo.plan.json`.

## Consequences

- Every approved release is reproduced on two runners (plan and apply). This
  is the cross-machine check letsgo's own CI already does, now on every
  release.
- Nothing built in the plan job is published, so a compromised artifact store
  between the jobs can't inject bytes.
- The plan file is small and readable, and reviewers see digests, not blobs.
- Apply needs the Go toolchain and pays a second build.
- A letsgo version change between plan and apply that alters the output is
  caught as a digest mismatch, with no separate version check needed.

## Alternatives considered

- **Bundle the bytes (Terraform-literal).** Rejected: apply would publish
  bytes it never reproduced, which is exactly what letsgo exists to avoid.
- **Both modes.** Rejected: two paths, and the weaker one would become the
  default.
