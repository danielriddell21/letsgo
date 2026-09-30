# letsgo docs

Proposals are split by audience:

| type | answers | format |
| --- | --- | --- |
| [PRD](prd/) | what and why, from the user's side | problem, solution, numbered user stories, decisions, testing, out of scope |
| [HLD](hld/) | how it works, and in what order | components, data, flow, interactions, delivery phases |
| [ADR](adr/) | why this way, not another | context, decision, consequences, alternatives (Nygard style) |
| [PBS](pbs/) | exactly what the product must do (product-based specification) | scope, interfaces, numbered MUST/SHOULD requirements traced to user stories, edge cases, Given/When/Then acceptance |

Each HLD opens with its status. Implemented designs are kept as the record of why; randomart, PGP words, receipt and plan/apply are still proposals.

## Index

| feature | issue | epic | PRD | HLD | PBS | ADRs |
| --- | --- | --- | --- | --- | --- | --- |
| Monorepo releases | [#24](https://github.com/danielriddell21/letsgo/issues/24) | [#38](https://github.com/danielriddell21/letsgo/issues/38) | [prd](prd/monorepo.md) | [hld](hld/monorepo.md) | [pbs](pbs/monorepo.md) | [0001](adr/0001-monorepo-scope-in-core.md), [0002](adr/0002-tag-prefix-from-module-dir.md) |
| Prerelease channels, promote | [#29](https://github.com/danielriddell21/letsgo/issues/29) | [#38](https://github.com/danielriddell21/letsgo/issues/38) | [prd](prd/promote.md) | [hld](hld/promote.md) | [pbs](pbs/promote.md) | [0009](adr/0009-promotion-rebuilds-at-final-tag.md), [0010](adr/0010-promote-creates-new-release.md), [0011](adr/0011-previous-release-by-kind.md) |
| Plan / apply | [#50](https://github.com/danielriddell21/letsgo/issues/50) | [#38](https://github.com/danielriddell21/letsgo/issues/38) | [prd](prd/plan-apply.md) | [hld](hld/plan-apply.md) | [pbs](pbs/plan-apply.md) | [0016](adr/0016-plan-is-intent-apply-rebuilds.md), [0017](adr/0017-stale-plan-by-observed-state.md), [0018](adr/0018-native-summary-terraform-export.md), [0019](adr/0019-plan-apply-enforcement-in-core.md) |
| Plugin-aware core | [#25](https://github.com/danielriddell21/letsgo/issues/25) | [#39](https://github.com/danielriddell21/letsgo/issues/39) | [prd](prd/integrated-plugins.md) | [hld](hld/integrated-plugins.md) | [pbs](pbs/integrated-plugins.md) | [0003](adr/0003-plugin-logic-in-core-thin-mains.md), [0004](adr/0004-core-writes-tap-files.md) |
| Feature toggles | [#26](https://github.com/danielriddell21/letsgo/issues/26) | [#39](https://github.com/danielriddell21/letsgo/issues/39) | [prd](prd/features.md) | [hld](hld/features.md) | [pbs](pbs/features.md) | [0005](adr/0005-feature-catalogue-disable-require.md) |
| Global config, `.letsgo/`, store | [#27](https://github.com/danielriddell21/letsgo/issues/27) | [#39](https://github.com/danielriddell21/letsgo/issues/39) | [prd](prd/config-dirs.md) | [hld](hld/config-dirs.md) | [pbs](pbs/config-dirs.md) | [0006](adr/0006-global-config-cannot-change-a-release.md), [0007](adr/0007-content-addressed-plugin-store.md) |
| Editor support | [#28](https://github.com/danielriddell21/letsgo/issues/28) | [#40](https://github.com/danielriddell21/letsgo/issues/40) | [prd](prd/vscode.md) | [hld](hld/vscode.md) | [pbs](pbs/vscode.md) | [0008](adr/0008-editor-binary-is-the-brain.md) |
| `letsgo doctor` | [#31](https://github.com/danielriddell21/letsgo/issues/31) | [#40](https://github.com/danielriddell21/letsgo/issues/40) | [prd](prd/doctor.md) | [hld](hld/doctor.md) | [pbs](pbs/doctor.md) | — |
| `letsgo audit` | [#30](https://github.com/danielriddell21/letsgo/issues/30) | [#41](https://github.com/danielriddell21/letsgo/issues/41) | [prd](prd/audit.md) | [hld](hld/audit.md) | [pbs](pbs/audit.md) | [0012](adr/0012-audit-results-append-only.md) |
| sumdb cross-check | [#32](https://github.com/danielriddell21/letsgo/issues/32) | [#41](https://github.com/danielriddell21/letsgo/issues/41) | [prd](prd/sumdb.md) | [hld](hld/sumdb.md) | [pbs](pbs/sumdb.md) | [0013](adr/0013-sumdb-check-against-proxy-zip.md) |
| What shipped | [#33](https://github.com/danielriddell21/letsgo/issues/33) | [#41](https://github.com/danielriddell21/letsgo/issues/41) | [prd](prd/what-shipped.md) | [hld](hld/what-shipped.md) | [pbs](pbs/what-shipped.md) | [0011](adr/0011-previous-release-by-kind.md) |
| Randomart | [#34](https://github.com/danielriddell21/letsgo/issues/34) | [#42](https://github.com/danielriddell21/letsgo/issues/42) | [prd](prd/randomart.md) | [hld](hld/randomart.md) | [pbs](pbs/randomart.md) | [0014](adr/0014-fingerprints-from-manifest-digest.md) |
| PGP words | [#35](https://github.com/danielriddell21/letsgo/issues/35) | [#42](https://github.com/danielriddell21/letsgo/issues/42) | [prd](prd/pgp-words.md) | [hld](hld/pgp-words.md) | [pbs](pbs/pgp-words.md) | [0014](adr/0014-fingerprints-from-manifest-digest.md) |
| Receipt | [#36](https://github.com/danielriddell21/letsgo/issues/36) | [#42](https://github.com/danielriddell21/letsgo/issues/42) | [prd](prd/receipt.md) | [hld](hld/receipt.md) | [pbs](pbs/receipt.md) | [0014](adr/0014-fingerprints-from-manifest-digest.md), [0015](adr/0015-easter-eggs-render-verify-result.md) |

## ADRs

| # | decision | status |
| --- | --- | --- |
| [0001](adr/0001-monorepo-scope-in-core.md) | Monorepo scope lives in core, not a hook plugin | accepted |
| [0002](adr/0002-tag-prefix-from-module-dir.md) | Tag prefix derived from module directory | accepted |
| [0003](adr/0003-plugin-logic-in-core-thin-mains.md) | Plugin logic in letsgo; plugins stay separate pinned binaries | accepted |
| [0004](adr/0004-core-writes-tap-files.md) | Core writes tap files; plugins only render | accepted |
| [0005](adr/0005-feature-catalogue-disable-require.md) | `disable`/`require` over a closed catalogue | accepted |
| [0006](adr/0006-global-config-cannot-change-a-release.md) | Global config cannot change what a release is | accepted |
| [0007](adr/0007-content-addressed-plugin-store.md) | Content-addressed plugin store | accepted |
| [0008](adr/0008-editor-binary-is-the-brain.md) | Editor support: the binary is the brain | accepted |
| [0009](adr/0009-promotion-rebuilds-at-final-tag.md) | Promotion rebuilds at the final tag | accepted |
| [0010](adr/0010-promote-creates-new-release.md) | Promote creates a new release; RC restored first | accepted |
| [0011](adr/0011-previous-release-by-kind.md) | Previous release chosen by kind | accepted |
| [0012](adr/0012-audit-results-append-only.md) | Audit results append-only, outside the integrity set | accepted |
| [0013](adr/0013-sumdb-check-against-proxy-zip.md) | sumdb check against the proxy zip, as a gate | accepted |
| [0014](adr/0014-fingerprints-from-manifest-digest.md) | Fingerprints derive only from the manifest digest | proposed |
| [0015](adr/0015-easter-eggs-render-verify-result.md) | Easter eggs render `verify.Result` | proposed |
| [0016](adr/0016-plan-is-intent-apply-rebuilds.md) | A plan holds intent; apply rebuilds and compares | proposed |
| [0017](adr/0017-stale-plan-by-observed-state.md) | Stale plan = tag moved or target in unplanned state | proposed |
| [0018](adr/0018-native-summary-terraform-export.md) | Native job summary; Terraform export is the `letsgo-tfplan` companion | proposed |
| [0019](adr/0019-plan-apply-enforcement-in-core.md) | Plan/apply enforcement in core; only presentation may be a plugin | proposed |
| [0020](adr/0020-plan-file-and-apply-surface.md) | The plan file, `apply`, and drift detection | accepted |

New ADRs take the next number. Superseded ADRs stay, with their status set to
`superseded by NNNN`.
