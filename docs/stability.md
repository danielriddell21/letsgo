# Stability

From v1.0.0, letsgo follows [semantic versioning][semver] for the surface
listed here. A change that breaks any of it is a major release; anything
added to it is a minor release; a fix is a patch. The reasoning and the
alternatives are in [ADR-0027](adr/0027-v1-compatibility-promise.md).

## What is covered

| surface | what stays the same within v1 |
| --- | --- |
| **Command line** | The commands and flags in the README's Usage section, their meanings, and their exit statuses (`2` from `plan --diff --exit-code` means the plan has changes). A flag may be added; none is removed or changes meaning. |
| **`letsgo.mod`** | Every directive and its arguments, and the defaults a repository gets with no config. A directive that parses today parses the same way. New directives are additive. `modsyntax` and `letsgo fmt` keep their grammar. |
| **Machine-readable output** | The JSON forms: `plan --json`, `version --json`, `plugin list --json`, `doctor --json`, `verify --json`, `diff --format json`, `features --json`. Fields are added, never renamed, removed or retyped. |
| **Files letsgo writes** | The manifest `letsgo.json` (schema 1), the plan file (schema 1), and the asset names a release publishes. A new schema number is a major release; readers that tolerate an unfamiliar number (`manifest.Decode`) keep doing so. |
| **Plugin wire contract** | The hooks `ldflags`, `archive-layout` and `tap-files`, their JSON input and output, and how a plugin finds its config ([ADR-0023](adr/0023-plugins-share-cores-wire-contract.md), [ADR-0025](adr/0025-core-owns-plugin-config-syntax.md)). A new hook is additive. |
| **Reading old releases** | `verify`, `diff`, `audit`, `promote` and `yank` keep reading the manifests of every earlier v1 release. |
| **Go API** | The exported identifiers of `github.com/danielriddell21/letsgo/plan`, `plugin`, `manifest`, `selfupdate` and `modsyntax`. letsgo's own release runs its API gate against the previous release, so a removal cannot ship by accident. |

## What is not covered

- Everything under `internal/`, and the `cmd/letsgo` package. These change
  freely.
- Human-readable output: the wording of messages, the layout of a plan, the
  text of release notes, colours and the receipt art. Use the JSON forms in
  scripts.
- The `capabilities` list in `version --json` grows over time; a client asks
  for the one it needs rather than comparing the whole list.
- The minimum Go version needed to build letsgo may rise in a minor release,
  as Go itself does for its own releases.
- A bug fix that changes behaviour the documentation never described.
- The companion repositories ([letsgo-plugins][], [letsgo-action][],
  [letsgo-vscode][]) version themselves. Each states the letsgo version it
  needs.

## Deprecation

Within v1, a feature is deprecated before it is removed: a `Deprecated:` doc
comment for Go API, and a note in the release notes and the command's own help
for anything else. Removal waits for the next major release.

[semver]: https://semver.org/
[letsgo-plugins]: https://github.com/danielriddell21/letsgo-plugins
[letsgo-action]: https://github.com/danielriddell21/letsgo-action
[letsgo-vscode]: https://github.com/danielriddell21/letsgo-vscode
