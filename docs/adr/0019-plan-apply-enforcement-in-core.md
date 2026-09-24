# ADR-0019: Plan/apply enforcement lives in core; only presentation may be a plugin

- Status: proposed
- Date: 2026-09-29
- Issue: [#50](https://github.com/danielriddell21/letsgo/issues/50)
- HLD: [hld/plan-apply.md](../hld/plan-apply.md#why-core-not-a-plugin)

## Context

Could plan/apply ship as a plugin (a `letsgo-plan` companion in
letsgo-plugins) instead of core? Plugins are either hooks (answering a
question core asks, with the answer recorded, never run by `verify`) or
hookless companions (they orchestrate the CLI and can never change bytes or
publishing). A companion can do a lot from outside:

| step | from outside core |
| --- | --- |
| predict digests | ✓ `letsgo build` writes `letsgo.json` |
| observe the forge and diff | ✗ the decision logic is the recorder, in core; a copy would drift (the #25 problem) |
| render, and Terraform output | ✓ and it could import unum |
| stale checks | ⚠ possible, but there's a time-of-check/time-of-use gap before `release` starts |
| refuse on a digest mismatch before publishing | ✗ `release` builds and publishes in one process; there's no seam to interpose |
| publish only the planned actions | ✗ a companion can't constrain what `release` does |
| record the plan in the manifest, and `verify` it | ✗ the manifest and verify are core |
| `release` = plan + apply | ✗ core |

## Decision

- **Core** owns everything that gives plan/apply its guarantee: the forge
  observation and diff (the recorder), the plan file schema, apply's
  rebuild-and-compare, the stale checks, the "planned actions only" limit,
  the manifest's `plan` record, `verify`, `plan -yank`, and the Markdown job
  summary.
- **Plugins** may own presentation and interop that read the plan file: the
  Terraform export and rendering (`letsgo-tfplan`,
  [ADR-0018](0018-native-summary-terraform-export.md)), and anything else
  that only reads.
- There's no plan/apply hook.

## Consequences

- "apply does exactly the plan" is enforced in the process that publishes,
  so it can't be bypassed by skipping an optional install.
- This follows the rule in [ADR-0001](0001-monorepo-scope-in-core.md) and
  [ADR-0006](0006-global-config-cannot-change-a-release.md): anything that
  decides what gets published is core.
- Core grows the plan and apply verbs. The recorder already exists, so
  mostly this is wiring.

## Alternatives considered

- **The whole thing as a `letsgo-plan` companion (an advisory plan).**
  Rejected: it would duplicate the recorder, leave a race between check and
  publish, be unable to stop unplanned actions, and leave no record verify
  can check. It would be a plan you have to trust, the opposite of letsgo.
- **Core exposes seams** (`release --expect-manifest`, `--plan-sha`,
  `release --snapshot --json` against the real forge) **and a companion
  drives them.** Rejected: the seams *are* the enforcement, so core would
  hold the logic anyway, and the companion would only add an install step.
- **A `plan-render` or `apply-gate` hook.** Rejected: hooks answer questions
  core asks while building; approving a release is a workflow step, not a
  question core asks.
