# ADR-0018: letsgo renders the job summary itself; the Terraform export is a companion plugin

- Status: proposed
- Date: 2026-09-29 (revised: the export moved from core to `letsgo-tfplan`)
- Issue: [#50](https://github.com/danielriddell21/letsgo/issues/50)
- HLD: [hld/plan-apply.md](../hld/plan-apply.md#rendering-and-interop)

## Context

The plan job's summary is what reviewers approve. `tf-plan-summary-action`
(built on `unum/pkg/terraform`) already renders Terraform plans well as a
coloured `diff` block. When tested:

- Given letsgo's native plan file, it reports a false "✅ No changes"
  (there's no `resource_changes` key).
- Given a Terraform-shaped export, it renders correctly, including
  `(known after apply)`.
- Its heading, preamble, no-change text and footer are hard-coded as
  Terraform.

letsgo has zero dependencies, so it can't import `unum/pkg/terraform`.
Terraform's plan JSON is someone else's schema.

## Decision

- **Core renders the job summary:** `plan --format md`, with change markers
  in column 0 inside a `diff` block (`+`, `-`, and `!` for changes).
  letsgo-action appends it to `$GITHUB_STEP_SUMMARY`. The summary is the
  evidence a reviewer approves, so it can't depend on an optional install.
- **The Terraform export is a hookless companion,** `letsgo-tfplan` in
  letsgo-plugins. It reads the native plan file and writes
  `terraform show -json`-shaped output (`resource_changes`, `letsgo_<kind>`
  types, before/after/after_unknown), or renders Markdown with
  `unum/pkg/terraform`.
- The native plan file schema becomes public API, as `letsgo/plan` alongside
  #25's exported packages, so the companion never redeclares it.
- `apply` reads only the native plan schema.

## Consequences

- Core carries no Terraform schema. The export can import unum, and renders
  exactly as `unum diff` and tf-plan-summary-action do, from one engine.
- Interop needs an extra install (`letsgo plugin install letsgo-tfplan`).
- `letsgo/plan` falls under letsgo's apidiff gate.
- letsgo-plugins gains a dependency on unum (its zero-dependency stance is
  already relaxed in #25).
- Follow-ups belong to tf-plan-summary-action: fail when `resource_changes`
  is missing, and add `title` and tool-name inputs.

## Alternatives considered

- **`plan --json=terraform` in core** (this ADR's first version). Rejected:
  core would own a foreign schema, and couldn't reuse the unum renderer.
- **letsgo-action calls tf-plan-summary-action for the summary.** Rejected:
  an extra build step, Terraform wording, and a silent false "no changes" if
  it's fed the wrong file.
- **Embedding `resource_changes` in the plan file.** Rejected: two schemas in
  one file.
- **A `plan-render` hook.** Rejected: hooks answer questions core asks while
  releasing; rendering an already-final plan isn't one of them. See
  [ADR-0019](0019-plan-apply-enforcement-in-core.md).
