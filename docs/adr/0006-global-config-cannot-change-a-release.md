# ADR-0006: Global config cannot change what a release is

- Status: proposed
- Date: 2026-09-24
- Issue: [#27](https://github.com/danielriddell21/letsgo/issues/27)
- HLD: [hld/config-dirs.md](../hld/config-dirs.md)

## Context

Machine settings (tool paths, cache, proxy, plugin mirror) exist only as
environment variables or constants. A global config file is wanted. The risk
is that a release would depend on who ran it.

## Decision

- `os.UserConfigDir()/letsgo/config.mod` has its **own closed directive
  table**. It covers only how this machine does the work.
- Anything that affects the bytes, gates, version or what is published is an
  error there, with the message "belongs in letsgo.mod".
- No tokens are stored in files. `token-command` asks a credential helper on
  demand.
- Precedence is flag > env > global > default, and each value's source is
  noted for `plan --explain`.
- Plugin config moves to `.letsgo/<plugin>.mod`. `letsgo.mod` stays at the
  root.

## Consequences

- A laptop, CI and `verify` still reach the same answer from the same commit.
- Org-wide release defaults have to come from a template repository or a
  shared workflow, not from global config.

## Alternatives considered

- **Global release defaults** (e.g. a shared `build` matrix). Rejected by the
  rule above.
- **`letsgo.mod` inside `.letsgo/`.** Rejected: it hides the release
  definition and breaks every repository.
- **A gitignored `.letsgo/local.mod`.** Rejected: env vars cover that case.
