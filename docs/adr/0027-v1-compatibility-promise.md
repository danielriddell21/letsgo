# ADR-0027: v1.0.0 promises compatibility for a named surface

- Status: accepted
- Date: 2026-10-06

## Context

letsgo has shipped as v0 since its first release, so every minor release was
free to break anything. Others now depend on it in four ways that a break would
hurt: projects whose `letsgo.mod` and CI invoke it, scripts that read its JSON,
plugins that speak its wire contract, and Go programs that import `plan`,
`plugin`, `manifest`, `selfupdate` and `modsyntax`.

The refactor that preceded v1 (issues #225 to #238) moved most of what lives in
`internal/` and reshaped `plan.Plan`. It was the last chance to do that for
free.

## Decision

- v1.0.0 promises semantic versioning for the surface listed in
  [stability.md](../stability.md): the command line, `letsgo.mod`, the JSON
  forms, the manifest and plan files, the plugin wire contract, reading earlier
  v1 releases and the five public Go packages.
- `internal/`, `cmd/letsgo`, human-readable output and the companion
  repositories are outside it.
- Deprecations last for at least one minor release before a major release may
  remove them.
- The module path stays `github.com/danielriddell21/letsgo`. Go only asks for a
  `/v2` suffix from v2, and nothing here needs one.
- `plugin.ReadConfig`, deprecated by [ADR-0025](0025-core-owns-plugin-config-syntax.md)
  for removal at the next breaking release, is removed in v1.0.0. `LoadConfig`
  replaces it.

## Consequences

- The README, SECURITY.md and CONTEXT.md point at one stability page rather
  than each making their own claims.
- Anything exported from the five public packages is now a commitment. New
  helpers go in `internal/` until a second caller outside the module needs
  them.
- The release's own API gate (`--allow-breaking`) is no longer routinely
  bypassed: a major bump is the only way a break ships.
- Supported versions in SECURITY.md become the latest v1 release.

- `letsgo tag` held a major back to a minor below v1, so it could never
  graduate a project. An explicit `--major` is now honoured: `letsgo tag
  --major --pre` proposes `v1.0.0-rc.1`, and a major inferred from commits or
  the API is still held back.

## Alternatives considered

- **Staying on v0.** Rejected: v0 tells users nothing is promised, and the
  surface is now settled enough to promise.
- **Promising the whole of `internal/` too.** Rejected: it would freeze the
  structure the refactor just finished improving, for no external caller's
  benefit.
- **Including human-readable output.** Rejected: it would make every message
  fix a breaking change. The JSON forms exist for scripts.
- **Keeping `ReadConfig` for the v1 line.** Rejected: it was deprecated for
  exactly this release, and keeping it would put a function with a legacy
  working-directory lookup behind the compatibility promise.
