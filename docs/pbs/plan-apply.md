# PBS: Terraform-style `plan` / `apply`

> Product-based specification: what the product must do, stated so it can be tested.
> [PRD](../prd/plan-apply.md) · [HLD](../hld/plan-apply.md) · Issue [#50](https://github.com/danielriddell21/letsgo/issues/50) · ADRs [0016](../adr/0016-plan-is-intent-apply-rebuilds.md), [0017](../adr/0017-stale-plan-by-observed-state.md), [0018](../adr/0018-native-summary-terraform-export.md), [0019](../adr/0019-plan-apply-enforcement-in-core.md)

## Scope

A reviewable release plan with forge diffs; applying a saved plan exactly;
yank plans; the plan record in the manifest; and the letsgo-action plan and
apply commands.

## Interfaces

| surface | specification |
| --- | --- |
| CLI | `letsgo plan [-out FILE] [-yank TAG] [--json] [--format text\|md]`, `letsgo apply [FILE] [-auto-approve]`, `letsgo release` (unchanged flags) |
| action line | `<op> <kind> <target> <detail>`; op ∈ `+` add, `~` change, `-` remove, `=` no change |
| kinds | `release`, `asset`, `tap`, `image`, `proxy`, `gomod` |
| footer | `Plan: <a> to add, <c> to change, <r> to remove.` (`=` not counted) |
| plan file | JSON `{schema: 1, letsgo_version, created_at, kind: "release"\|"yank", repo, tag, commit, config_sha256, manifest, actions: [{op, kind, target, observed, planned}]}` |
| manifest | `plan: {sha256, created_at, letsgo_version}` |
| release asset | `letsgo.plan.json` |
| markdown | a header and footer outside a ```` ```diff ```` block; inside it, markers in column 0: `+` add, `-` remove, `!` change; `=` omitted and counted in the footer |
| Go package | `letsgo/plan`: exported plan file types and `Read` (public API, apidiff-gated) |
| companion (letsgo-plugins) | `letsgo-tfplan json <plan>` (`terraform show -json` shape: `{format_version, resource_changes: [{address, type: "letsgo_<kind>", name, change: {actions, before, after, after_unknown}}]}`), `letsgo-tfplan md <plan>` |
| letsgo-action | `command: plan` (artifact `release.plan`, job summary), `command: apply` (downloads the artifact) |

## Requirements

| ID | requirement | story |
| --- | --- | --- |
| PA-1 | `plan` MUST read the forge (release, assets, tap files, image tags) read-only, and MUST NOT write anything. | 1 |
| PA-2 | `plan` MUST render one action line per target and the summary footer. | 2 |
| PA-3 | `plan -out` MUST write the plan file, and print its sha256 and the apply command. | 3 |
| PA-4 | The plan MUST contain every predicted artifact digest and MUST NOT contain artifact bytes. | 3 |
| PA-5 | `apply` MUST rebuild, and MUST refuse before any forge write if any artifact digest differs from the plan, listing the differences. | 4 |
| PA-6 | `apply` MUST refuse if the tag no longer resolves to the plan's commit. | 5 |
| PA-7 | `apply` MUST refuse if any target's current state is neither `observed` nor `planned`, naming each such target. | 6 |
| PA-8 | `apply` MUST skip targets already in their planned state, and succeed. | 7 |
| PA-9 | `apply` MUST perform no forge write that isn't in the plan's actions. | 4 |
| PA-10 | The published manifest MUST include `plan.sha256` equal to the sha256 of the attached `letsgo.plan.json`. | 9 |
| PA-11 | The rebuild comparison MUST exclude the manifest's `plan` field. | 4 |
| PA-12 | `verify` MUST print the plan record, and check that the attached plan's sha256 matches it. | 10 |
| PA-13 | `plan -yank TAG` MUST produce a `kind: "yank"` plan covering the retract, formula, cask and `@next` rollbacks, and the release marking. | 11 |
| PA-14 | `release` MUST produce the same forge calls as `plan -out` followed by `apply` for the same state. | 12 |
| PA-15 | `apply` without a file MUST render the plan and prompt on a TTY; without a TTY it MUST require `-auto-approve`. | 13 |
| PA-16 | `plan --json` MUST output the plan file's schema. | 14 |
| PA-17 | letsgo-action `command: plan` MUST upload the plan as an artifact and write the rendered plan to the job summary. `command: apply` MUST apply that artifact. | 8 |
| PA-18 | `plan --format md` MUST put the change markers in column 0 inside a `diff` block, using `!` for changes. | 15 |
| PA-19 | The plan file types MUST be exported as `letsgo/plan`, and changes MUST pass apidiff. | 16 |
| PA-20 | Core MUST NOT emit Terraform-format JSON. `letsgo-tfplan json` MUST map `+ ~ - =` to `create`/`update`/`delete`/`no-op`, give before and after state as attribute maps, and mark digests unknown at plan time in `after_unknown`. | 16 |
| PA-21 | `apply` MUST accept only the native plan schema, never the Terraform export. | 4 |
| PA-22 | Nothing outside core (no plugin, no companion) may change what `apply` does. | 4 |

## Errors and edge cases

- A plan file with an unknown schema: refuse.
- A plan for a different repository than the checkout: refuse.
- Nothing to do (every action is `=`): apply exits 0 and makes no writes.
- A plan with failing gates: `-out` isn't written, and plan exits non-zero.
- Promote (#29): `plan -promote` is a follow-up. ADR-0010's unconditional RC
  restore still applies when apply refuses.

## Acceptance scenarios

1. **Given** a new tag and an existing formula, **when** `plan` runs, **then** it shows `+ release`, `+ asset` lines, `~ tap Formula/x.rb`, and the correct footer, with no forge writes. (PA-1, PA-2)
2. **Given** a saved plan, **when** apply runs on another machine, **then** the digests match, the release is published, and the manifest's `plan.sha256` equals the attached plan's sha256. (PA-5, PA-10)
3. **Given** a saved plan and a changed toolchain on the apply runner, **when** apply runs, **then** it refuses before any write, listing the differing digests. (PA-5)
4. **Given** a saved plan and the tag force-moved, **when** apply runs, **then** it refuses with "tag moved". (PA-6)
5. **Given** a saved plan and someone editing the formula by hand, **when** apply runs, **then** it refuses, naming `Formula/x.rb`. (PA-7)
6. **Given** an apply that failed after uploading 3 of 6 assets, **when** apply is re-run with the same plan, **then** it uploads only the remaining 3 and succeeds. (PA-8)
7. **Given** `plan -yank v1.3.0 -out y.plan`, **when** `apply y.plan` runs, **then** exactly the planned retract and rollbacks happen. (PA-13)
8. **Given** `letsgo-tfplan json release.plan` output, **when** it's passed to tf-plan-summary-action or `unum diff`, **then** each action renders with the right marker and before/after values. (PA-20)
9. **Given** letsgo-action `command: plan`, **when** the job finishes, **then** the job summary shows the Markdown plan with coloured `+`, `-` and `!` lines. (PA-18, PA-17)
