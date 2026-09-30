# HLD: Terraform-style `plan` / `apply`

Status: proposal. Nothing here is implemented.

| | |
|---|---|
| Issue | [#50](https://github.com/danielriddell21/letsgo/issues/50) |
| Epic | [#38](https://github.com/danielriddell21/letsgo/issues/38) |
| PRD | [prd/plan-apply.md](../prd/plan-apply.md) |
| PBS | [pbs/plan-apply.md](../pbs/plan-apply.md) |
| ADRs | [ADR-0016](../adr/0016-plan-is-intent-apply-rebuilds.md), [ADR-0017](../adr/0017-stale-plan-by-observed-state.md), [ADR-0018](../adr/0018-native-summary-terraform-export.md), [ADR-0019](../adr/0019-plan-apply-enforcement-in-core.md) |

## Where things are today

- `letsgo plan` resolves config and runs the gates. It never contacts the
  forge unless `--publish` is passed, and even then it only checks access.
- `letsgo release --snapshot` runs the whole release against `Recorder`, a
  `Forge` that records calls instead of making them
  (`internal/publish/recorder.go`). Its principle is that a dry run taking its
  own path proves nothing.
- `letsgo release` builds and publishes in one step, and the publisher is
  idempotent (existing matching assets are kept, and mismatches replaced).

## Shape

```
letsgo plan  [-out FILE] [-yank TAG] [--json]
letsgo apply [FILE] [-auto-approve]
letsgo release           # = plan + apply -auto-approve, one process
```

### Plan

1. Resolve (as today): config, version, gates.
2. **Build to a scratch directory** and compute the predicted manifest. The
   bytes are discarded after hashing. Only the digests go in the plan.
3. **Observe** the forge, read-only: the release by tag, its assets and
   digests, the tap files' blob shas, and the image tags' digests.
4. **Diff** the intended state against the observed state using the recorder's
   decision code, producing actions.
5. Render the actions, and with `-out` write the plan file.

```
letsgo plan  you/gambit v1.3.0 @ 4f2a9c1

  + release   v1.3.0                         (latest, notes 2.1 KB)
  + asset     gambit_1.3.0_linux_amd64.tar.gz   sha256:ab12…
  + asset     gambit_1.3.0_darwin_arm64.tar.gz  sha256:77e0…
  + asset     letsgo.json · SHA256SUMS · sbom · source · install.sh
  ~ tap       Formula/gambit.rb              blob 9c1e… → 3d7a…
  + image     ghcr.io/you/gambit:1.3.0       sha256:e4f1…
  ~ image     ghcr.io/you/gambit:latest      sha256:0b9d… → sha256:e4f1…
  = image     ghcr.io/you/gambit:1           (already e4f1…)
  + proxy     warm example.com/gambit@v1.3.0

  Plan: 7 to add, 2 to change, 0 to remove.
  Saved to release.plan (sha256:5c0e…). Apply with: letsgo apply release.plan
```

### Plan file (`letsgo.plan.json`)

```json
{
  "schema": 1,
  "letsgo_version": "v0.9.0",
  "created_at": "2026-09-27T10:00:00Z",
  "kind": "release",
  "repo": "you/gambit", "tag": "v1.3.0", "commit": "4f2a9c1…",
  "config_sha256": "…",
  "manifest": { "…": "the predicted letsgo.json, without the plan field" },
  "actions": [
    {"op": "+", "kind": "asset", "target": "gambit_1.3.0_linux_amd64.tar.gz",
     "observed": null, "planned": "sha256:ab12…"},
    {"op": "~", "kind": "tap", "target": "Formula/gambit.rb",
     "observed": "blob:9c1e…", "planned": "blob:3d7a…"}
  ]
}
```

- `observed` and `planned` are per-target state fingerprints: an asset digest,
  a blob sha, an image digest, or "absent".
- The plan's digest is the sha256 of its canonical JSON.

### Apply

1. Read the plan and check the schema.
2. **Stale checks** ([ADR-0017](../adr/0017-stale-plan-by-observed-state.md)):
   - `tag` must resolve to `commit`;
   - for every action, the target's current state must equal `observed`
     (nothing has happened yet) or `planned` (already done, e.g. a partial
     earlier apply). Anything else means **stale**, with the target named.
3. **Rebuild** in `dist/`, and compare every artifact digest with the plan's
   manifest ([ADR-0016](../adr/0016-plan-is-intent-apply-rebuilds.md)). Any
   difference refuses, listing the fields.
4. Write the manifest with `plan: {sha256, created_at, letsgo_version}` added.
   The comparison excludes this field, since the plan can't contain its own
   digest.
5. Perform **only** the planned actions that aren't already in their planned
   state, and attach `letsgo.plan.json` to the release.
6. Run the post-publish checks as today (proxy warm, and sumdb #32).

`apply` with no file = plan, render, and prompt `Apply? [y/N]` on a TTY. In a
non-interactive session it requires `-auto-approve`.

### Yank

`letsgo plan -yank v1.3.0 [-out]`:

```
  ~ go.mod     retract v1.3.0            (commit on trunk)
  ~ tap        Formula/gambit.rb        → v1.2.8
  ~ tap        Formula/gambit@next.rb   → v1.2.8   (#29)
  ~ tap        Casks/gambit.rb          → v1.2.8   (#25)
  ~ release    v1.3.0                    mark yanked in notes
  Plan: 0 to add, 5 to change, 0 to remove.
```

The plan's `kind` is `"yank"`. There's no manifest to predict, so staleness
is checked on the forge targets and the `go.mod` blob only.

### Snapshot

`release --snapshot` becomes `plan` against a forge that assumes nothing
exists, printed without `-out`. The recorder stays as the implementation of
"what would happen". Plan wraps it; it doesn't replace it.

## CI (letsgo-action)

```yaml
on: { push: { tags: ['v*'] } }
jobs:
  plan:
    runs-on: ubuntu-latest
    permissions: { contents: read }
    steps:
      - uses: actions/checkout@v7
        with: { fetch-depth: 0 }
      - uses: actions/setup-go@v7
        with: { go-version-file: go.mod }
      - uses: danielriddell21/letsgo-action@v1
        with: { command: plan }   # uploads release.plan, writes job summary
  apply:
    needs: plan
    environment: release          # required reviewers approve here
    runs-on: ubuntu-latest        # a different runner from plan, on purpose
    permissions: { contents: write, id-token: write, attestations: write, packages: write }
    steps:
      - uses: actions/checkout@v7
        with: { fetch-depth: 0 }
      - uses: actions/setup-go@v7
        with: { go-version-file: go.mod }
      - uses: danielriddell21/letsgo-action@v1
        with: { command: apply }  # downloads release.plan
```

- The plan job needs only read access, so the approval gate is also a
  privilege boundary.
- The job summary is the rendered plan, which is what the reviewer approves.
- There's no separate letsgo-action issue. #50 covers `command: plan` and
  `command: apply`.

## Rendering and interop

### Job summary: rendered by letsgo

`letsgo plan --format md` renders the plan as Markdown for
`$GITHUB_STEP_SUMMARY`. The header and footer sit outside a ```` ```diff ````
block, and inside it every change marker is in column 0 so GitHub colours
the lines: `+` add, `-` remove, and `!` change (`~` isn't coloured by diff
highlighters). letsgo-action `command: plan` appends this output. There's no
extra step and no extra build ([ADR-0018](../adr/0018-native-summary-terraform-export.md)).

```diff
  # tap Formula/gambit.rb will be changed
! tap "Formula/gambit.rb" {
!   blob    = "9c1e" -> "3d7a"
!   version = "1.2.8" -> "1.3.0"
! }
```

### Terraform-compatible export: the `letsgo-tfplan` companion

The export isn't in core ([ADR-0018](../adr/0018-native-summary-terraform-export.md)).
`letsgo-tfplan`, a hookless companion in letsgo-plugins, reads the native plan
file through the exported `letsgo/plan` types. It can import unum, so its
rendering is identical to `unum diff` and tf-plan-summary-action.

```
letsgo-tfplan json release.plan > plan.tf.json   # terraform show -json shape
letsgo-tfplan md   release.plan                  # unum/pkg/terraform renderer
```

Mapping:

| letsgo action | `resource_changes[]` |
| --- | --- |
| `kind` | `type`: `letsgo_<kind>` (`letsgo_release`, `letsgo_asset`, `letsgo_tap_file`, `letsgo_image_tag`, `letsgo_proxy`, `letsgo_gomod`) |
| `target` | `name` (verbatim), `address` = `<type>.<slug>` |
| `+ ~ - =` | `change.actions`: `["create"]`, `["update"]`, `["delete"]`, `["no-op"]` |
| observed / planned state | `change.before` / `change.after`, as attribute maps (`sha256`, `size`, `blob`, `version`, `digest`, …) |
| digest not yet known (`--no-build`) | `change.after_unknown.<attr> = true`, which renders "(known after apply)" |

The native plan file stays the source of truth. `apply` reads only the
native schema. The companion only reads it, and never affects apply
([ADR-0019](../adr/0019-plan-apply-enforcement-in-core.md)).

### tf-plan-summary-action: findings

Tested against `danielriddell21/tf-plan-summary-action` (unum v1.8.2):

- **The native plan file renders a false "✅ No changes"**, because there's
  no `resource_changes` key. That's why it isn't used for the job summary.
- **A Terraform-shaped export renders correctly:** create, update, delete,
  `(known after apply)`, and hidden no-ops.
- **Its wording is hard-coded:** "## Terraform Plan", "Terraform will
  perform…", "infrastructure is up to date", and "to destroy / to replace".
- **Follow-ups for that action** (in its own repository, not letsgo): fail
  when `resource_changes` is missing, and add `title` and tool-name inputs
  for the heading, preamble and no-change text.
- letsgo can't import `unum/pkg/terraform` (zero dependencies), and doesn't
  need to: core's renderer is a few dozen lines over the plan's actions.
  `letsgo-tfplan` is where unum is used.

## Why core, not a plugin

The guarantee of plan/apply is that apply does exactly the plan. Everything
that enforces it has to run in the process that publishes: the forge diff
(the recorder), rebuild-and-compare before any write, the stale checks,
"planned actions only", and the manifest `plan` record that `verify` checks.
A companion can't insert itself between `release`'s build and publish, can't
stop an unplanned action, and would race between its check and the publish.
That would make an advisory plan you have to trust.

So core owns enforcement, and plugins may own presentation that only reads
the plan file (`letsgo-tfplan`). There's no hook
([ADR-0019](../adr/0019-plan-apply-enforcement-in-core.md)).


## Interactions

- **#29 promote:** `plan -promote v1.3.0-rc.1` shows restore RC → tag → create
  release → brew and Docker. Apply's rebuild-and-compare is shared.
  ADR-0010's rule stays: restoring the RC is unconditional, even when apply
  refuses.
- **#24 monorepo:** one plan per module; the matrix runs plan and apply per
  module.
- **#26 features:** disabled features produce no actions, and the plan header
  lists departures from the defaults.
- **#33 what shipped:** the plan can show the "what shipped" table under the
  header.
- **#28 `--json`:** `plan --json` is the schema-1 plan file, rendered to
  stdout.
- **#25 plugins:** `letsgo/plan` joins the exported packages; `letsgo-tfplan`
  joins the first-party catalogue as a hookless companion.
- **#34–#36 fingerprints:** applied from the final manifest, after the plan
  field is added.

## Delivery

Phases, in order. Each is a vertical slice: a thin path through every layer, verified end to end. The behaviour each phase must meet is specified in the [PBS](../pbs/plan-apply.md).

1. Forge observation and the diff, in `plan` output (user stories 1, 2, 14)
2. `-out` and `apply` with rebuild-and-compare (user stories 3, 4, 12, 13)
3. Stale checks and partial re-apply (user stories 5, 6, 7)
4. Plan provenance in the manifest and verify (user stories 9, 10)
5. letsgo-action `plan` and `apply` plus the environment example (user story 8)
6. `plan -yank` (user story 11)
7. `plan --format md` in core; export `letsgo/plan` types (user story 15)
8. `letsgo-tfplan` companion in letsgo-plugins (user story 16)

## Open questions

- Should `plan` without `-out` still build (to predict digests), or offer
  `--no-build` for a fast, forge-only diff that marks digests "known after
  apply" (Terraform's term)? Proposed: build by default; `--no-build` shows
  `(known after apply)`.
- Should the plan file be signed (Sigstore) in CI? Proposed: not in v1. It's
  an artifact of the same workflow run, and apply rebuilds anyway.
