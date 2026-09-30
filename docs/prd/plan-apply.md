# PRD: Terraform-style `plan` / `apply`

| | |
|---|---|
| Issue | [#50](https://github.com/danielriddell21/letsgo/issues/50) |
| Epic | [#38](https://github.com/danielriddell21/letsgo/issues/38) |
| HLD | [hld/plan-apply.md](../hld/plan-apply.md) |
| PBS | [pbs/plan-apply.md](../pbs/plan-apply.md) |
| ADRs | [0016](../adr/0016-plan-is-intent-apply-rebuilds.md), [0017](../adr/0017-stale-plan-by-observed-state.md), [0018](../adr/0018-native-summary-terraform-export.md), [0019](../adr/0019-plan-apply-enforcement-in-core.md) |

## Problem Statement

A tag push goes straight to publishing. Nobody reviews what is about to
happen: which assets will be created or replaced, whether the stable formula
will change, which Docker tags will move. `release --snapshot` rehearses a
release, but it can't be approved and then carried out as approved.
Something could change between the rehearsal and the real run, and nothing
would notice.

## Solution

`letsgo plan -out release.plan` resolves the release, predicts every artifact
digest, and diffs the intended forge state against the actual forge state.
The output uses Terraform-style symbols. `letsgo apply release.plan` rebuilds,
refuses unless every digest matches the plan and the forge hasn't drifted,
then performs exactly the planned actions. The manifest records which plan
was applied. In CI, the plan job produces the plan, and a reviewer approves
the apply job through a GitHub environment. Yanks go through the same flow.

## User Stories

1. As a maintainer, I want to see every forge change a release will make before it happens, so that I can catch mistakes.
2. As a maintainer, I want Terraform-style `+ ~ - =` lines and a summary count, so that the plan is familiar and scannable.
3. As a maintainer, I want to save the plan with `-out`, so that what I approve is what runs.
4. As a maintainer, I want apply to rebuild and refuse if any digest differs from the plan, so that nothing unreviewed ships.
5. As a maintainer, I want apply to refuse when the tag has moved since the plan, so that a force-pushed tag can't sneak through.
6. As a maintainer, I want apply to refuse when someone else changed the release, tap or image tags since the plan, so that I never overwrite unseen changes.
7. As a maintainer, I want re-applying after a partial failure to work, so that a network blip doesn't force a new plan.
8. As a reviewer, I want the rendered plan in the CI job summary and an environment approval gate, so that I approve from the GitHub UI.
9. As a consumer, I want the manifest to record the applied plan's digest, and the plan to be attached, so that I can see what was approved.
10. As a consumer, I want `verify` to show the plan record, so that approval is part of the evidence.
11. As a maintainer, I want `letsgo plan -yank v1.3.0` to show exactly what a yank will do, so that yanks are reviewed too.
12. As an existing user, I want `letsgo release` to keep working, so that my workflows don't break.
13. As a maintainer running locally, I want `letsgo apply` with no file to show the plan and ask for confirmation, so that I get the same safety locally.
14. As a script author, I want `plan --json`, so that I can gate on the actions.
15. As a reviewer, I want the job summary to colour adds, changes and removals, so that I can approve at a glance.
16. As a Terraform user, I want to export a letsgo plan in Terraform's plan JSON format, so that my existing tools (`unum diff`, tf-plan-summary-action, tf-summarize) can show it.

## Implementation Decisions

- The plan holds intent (the predicted manifest and the actions), not bytes,
  and apply rebuilds and compares
  ([ADR-0016](../adr/0016-plan-is-intent-apply-rebuilds.md)).
- A plan is stale if the tag or commit moved, or if a target is in neither
  its observed state nor its planned state
  ([ADR-0017](../adr/0017-stale-plan-by-observed-state.md)).
- The `--snapshot` recorder becomes the diff engine, so plan and apply use one
  code path (the existing "a dry run that takes its own path proves nothing"
  rule).
- `release` = plan + apply with auto-approve, in one process.
- The manifest gains `plan: {sha256, created_at, letsgo_version}`, and the plan
  file is attached as `letsgo.plan.json`.
- The job summary is rendered by core (`plan --format md`). The Terraform-compatible export is the `letsgo-tfplan` companion in letsgo-plugins, reading the exported `letsgo/plan` types ([ADR-0018](../adr/0018-native-summary-terraform-export.md)).
- Enforcement (diff, rebuild-and-compare, stale checks, planned actions only, the manifest record) is core; only read-only presentation may be a plugin ([ADR-0019](../adr/0019-plan-apply-enforcement-in-core.md)).
- letsgo-action gains `command: plan` (artifact plus job summary) and
  `command: apply`. The approval gate is a documented GitHub environment. This
  is covered here, with no separate issue on letsgo-action.

## Testing Decisions

- Golden plan output (text and JSON) for: a new release, a re-run with nothing
  to do, a changed formula, and a yank.
- Apply refuses on: a digest mismatch, a moved tag, and forge drift (each
  target kind).
- Apply succeeds on a re-run after a failure injected midway.
- `release` and `plan` + `apply` produce identical forge calls (recorder
  comparison).
- The manifest's `plan.sha256` equals the sha256 of the attached plan.

## Out of Scope

- Remote plan storage or locking (a Terraform backend).
- Applying someone else's plan across repositories.
- A plan age limit (not wanted; staleness is about state, not time).

## Further Notes

- Promote (#29) fits as `plan -promote <rc>`. Its rebuild-and-compare is the
  same machinery. See the HLD.
