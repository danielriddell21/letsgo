# ADR-0009: Promotion rebuilds at the final tag and proves the rebuild

- Status: proposed
- Date: 2026-09-24
- Issue: [#29](https://github.com/danielriddell21/letsgo/issues/29)
- HLD: [hld/promote.md](../hld/promote.md)

## Context

Promoting `v1.3.0-rc.1` to `v1.3.0` can either copy the RC's artifacts under
a new name, or rebuild them at the new tag. A copied binary would report
`1.3.0-rc.1`, and its manifest would describe a different tag.

## Decision

- `letsgo promote <rc>` tags `v1.3.0` on the **RC's commit** and rebuilds it.
- It compares the result with the RC manifest. The only differences allowed
  are the injected version string, archive names, and the digests those
  imply. The commit, Go version, flags, dependencies, targets and plugin
  records must all match.
- The stable manifest records
  `promoted_from: {tag, manifest_sha256}`.

## Consequences

- The binary reports its real version, and `verify v1.3.0` works as it does
  for any other release.
- "What was tested is what shipped" is checked mechanically, not assumed.
- Promotion costs a full build.

## Alternatives considered

- **Copy the RC's assets.** Rejected: the version is wrong, and the manifest
  and tag would disagree.
- **Retag only (`docker tag`-style).** Rejected for the same reason.
