# letsgo docs

Proposals are split by audience:

| type | answers | format |
| --- | --- | --- |
| [PRD](prd/) | what and why, from the user's side | problem, solution, numbered user stories, decisions, testing, out of scope |
| [HLD](hld/) | how it works, and in what order | components, data, flow, interactions, delivery phases |
| [ADR](adr/) | why this way, not another | context, decision, consequences, alternatives (Nygard style) |
| [PBS](pbs/) | exactly what the product must do (product-based specification) | scope, interfaces, numbered MUST/SHOULD requirements traced to user stories, edge cases, Given/When/Then acceptance |

All of them are proposals. Nothing here is implemented until its issue closes.

## Index

| feature | issue | epic | PRD | HLD | PBS | ADRs |
| --- | --- | --- | --- | --- | --- | --- |
| Monorepo releases | [#24](https://github.com/danielriddell21/letsgo/issues/24) | [#38](https://github.com/danielriddell21/letsgo/issues/38) | [prd](prd/monorepo.md) | [hld](hld/monorepo.md) | [pbs](pbs/monorepo.md) | [0001](adr/0001-monorepo-scope-in-core.md), [0002](adr/0002-tag-prefix-from-module-dir.md) |
| Prerelease channels, promote | [#29](https://github.com/danielriddell21/letsgo/issues/29) | [#38](https://github.com/danielriddell21/letsgo/issues/38) | [prd](prd/promote.md) | [hld](hld/promote.md) | [pbs](pbs/promote.md) | [0009](adr/0009-promotion-rebuilds-at-final-tag.md), [0010](adr/0010-promote-creates-new-release.md), [0011](adr/0011-previous-release-by-kind.md) |
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
| [0001](adr/0001-monorepo-scope-in-core.md) | Monorepo scope lives in core, not a hook plugin | proposed |
| [0002](adr/0002-tag-prefix-from-module-dir.md) | Tag prefix derived from module directory | proposed |
| [0003](adr/0003-plugin-logic-in-core-thin-mains.md) | Plugin logic in letsgo; plugins stay separate pinned binaries | proposed |
| [0004](adr/0004-core-writes-tap-files.md) | Core writes tap files; plugins only render | proposed |
| [0005](adr/0005-feature-catalogue-disable-require.md) | `disable`/`require` over a closed catalogue | proposed |
| [0006](adr/0006-global-config-cannot-change-a-release.md) | Global config cannot change what a release is | proposed |
| [0007](adr/0007-content-addressed-plugin-store.md) | Content-addressed plugin store | proposed |
| [0008](adr/0008-editor-binary-is-the-brain.md) | Editor support: the binary is the brain | proposed |
| [0009](adr/0009-promotion-rebuilds-at-final-tag.md) | Promotion rebuilds at the final tag | proposed |
| [0010](adr/0010-promote-creates-new-release.md) | Promote creates a new release; RC restored first | proposed |
| [0011](adr/0011-previous-release-by-kind.md) | Previous release chosen by kind | proposed |
| [0012](adr/0012-audit-results-append-only.md) | Audit results append-only, outside the integrity set | proposed |
| [0013](adr/0013-sumdb-check-against-proxy-zip.md) | sumdb check against the proxy zip, as an alarm | proposed |
| [0014](adr/0014-fingerprints-from-manifest-digest.md) | Fingerprints derive only from the manifest digest | proposed |
| [0015](adr/0015-easter-eggs-render-verify-result.md) | Easter eggs render `verify.Result` | proposed |

New ADRs take the next number. Superseded ADRs stay, with their status set to
`superseded by NNNN`.
