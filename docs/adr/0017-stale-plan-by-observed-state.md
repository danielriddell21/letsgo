# ADR-0017: A plan is stale when its tag moved or a target is in an unplanned state

- Status: accepted
- Date: 2026-09-27
- Issue: [#50](https://github.com/danielriddell21/letsgo/issues/50)
- HLD: [hld/plan-apply.md](../hld/plan-apply.md)

## Context

Time passes between plan and apply, while a reviewer approves. In that
window the tag could be force-moved, someone could publish by hand, or a
previous apply could have failed halfway. Terraform refuses a stale saved
plan. letsgo's publisher is idempotent, so "already done" isn't the same as
"changed by someone else".

## Decision

For each action, the plan records the target's **observed** state
fingerprint (at plan time) and its **planned** state fingerprint. At apply:

- the tag must still resolve to the planned commit;
- each target's current state must equal `observed` or `planned`;
- anything else makes the plan **stale**. Apply refuses before doing
  anything and names each drifted target.

Targets already in their planned state are skipped. There's no age limit
and no letsgo-version check (ADR-0016's digest compare covers that).

## Consequences

- Re-applying after a partial failure works without a new plan.
- A hand edit to the formula, or a release someone else created, blocks apply
  instead of being overwritten.
- The check requires reading every target at apply time, the same reads plan
  made.

## Alternatives considered

- **A plan age limit.** Rejected: staleness is about state, not time.
- **Refusing any change since plan.** Rejected: it breaks re-apply after a
  partial failure.
- **A forge lock during approval.** Rejected: GitHub has no such primitive,
  and it would need a backend.
