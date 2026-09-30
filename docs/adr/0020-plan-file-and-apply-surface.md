# ADR-0020: The plan file, `apply`, and drift detection

- Status: accepted
- Date: 2026-09-30
- Issue: [#50](https://github.com/danielriddell21/letsgo/issues/50)
- HLD: [hld/plan-apply.md](../hld/plan-apply.md)

## Context

[ADR-0016](0016-plan-is-intent-apply-rebuilds.md) says a plan holds intent and
apply rebuilds. It leaves open the command surface and what the file must
carry for apply to refuse before any write.

## Decision

- `letsgo plan -out <file>` writes the plan and implies `--diff`. The name is
  required: there is no default file.
- The file is JSON, schema 1, unsigned, and holds no secrets. It carries the
  tag, commit, the predicted manifest and its sha256, and the actions. Its
  digest is the sha256 of its compact JSON.
- `-out` runs the analysis gates, because the manifest records them and apply
  must reproduce it. `--diff` does too, so its asset digests are the ones a
  release would publish.
- `letsgo apply <file>` takes a file and nothing else. It rebuilds, and
  before any forge write refuses unless HEAD is the plan's tag at the plan's
  commit and the rebuilt manifest's sha256 equals the plan's. A refusal lists
  the manifest fields that differ.
- `letsgo plan --diff --exit-code` exits 2 when any action is not `=`, so a
  schedule can detect drift. Exit 1 stays for errors.

## Consequences

- Apply is one more build, and the same code path as `release`, so the two
  cannot publish differently.
- `apply` with no file is an error until the prompt of the HLD lands.
- A plan made by a different letsgo, toolchain or dependency set is refused
  by the digest, not by a separate version check.
- Apply reads the forge again after the rebuild matches. A target that is
  neither where the plan found it nor where it would leave it makes the plan
  stale ([ADR-0017](0017-stale-plan-by-observed-state.md)): apply names
  each one and writes nothing. A target already at its planned state is
  skipped, so re-applying after a partial failure needs no new plan.
- Apply writes through a guard that refuses any release, asset or tap write the
  plan did not list. Container image tags are covered by the staleness check
  alone.

## Alternatives considered

- **Default file name.** Rejected: a stale `letsgo.plan` in a working
  directory is an easy thing to apply by accident.
- **Signing the file.** Deferred: the rebuild is the integrity check, and a
  forged plan can only make apply refuse or do what `release` would.
