# PBS: Global config, `.letsgo/` and the plugin store

> Product-based specification: what the product must do, stated so it can be tested.
> [PRD](../prd/config-dirs.md) · [HLD](../hld/config-dirs.md) · Issue [#27](https://github.com/danielriddell21/letsgo/issues/27) · ADRs [0006](../adr/0006-global-config-cannot-change-a-release.md), [0007](../adr/0007-content-addressed-plugin-store.md)

## Scope

The content-addressed plugin store, `.letsgo/<plugin>.mod`, and the global
`config.mod`.

## Interfaces

| surface | specification |
| --- | --- |
| store path | `$XDG_DATA_HOME/letsgo/plugins/sha256/<digest>/<name>`, or the `plugins` directive |
| CLI | `plugin install` (no arguments means every pin), `plugin install --link`, `plugin prune`, `plugin list` (shows unreferenced entries) |
| project | `.letsgo/<short name>.mod`; legacy `letsgo-<name>.mod` at the root |
| hook input | `config_dir` (absolute path) |
| global file | `os.UserConfigDir()/letsgo/config.mod`, or `LETSGO_CONFIG` |
| global directives | `go`, `git`, `tool <name> <path>`, `cache <dir>\|off`, `plugins <dir>`, `plugin-repo <owner/name>`, `proxy <url>`, `token-command <argv...>`, `color auto\|always\|never`, `update-check off\|daily\|weekly` |

## Requirements

| ID | requirement | story |
| --- | --- | --- |
| CD-1 | `plugin install` MUST write into the store at the binary's digest path. | 1 |
| CD-2 | `plugin.Run` MUST look up the pinned digest in the store first, then `PATH`, and MUST re-hash before running either way. | 1 |
| CD-3 | Two different digests with the same name MUST coexist. | 1 |
| CD-4 | `plugin install` with no arguments MUST install every pin in `letsgo.mod`. | 2 |
| CD-5 | `plugin prune` MUST remove only store entries that no pin in the current repository references. | 4 |
| CD-6 | Hook input MUST include `config_dir`. | 12 |
| CD-7 | A legacy root plugin file alone MUST produce a Warn; both files together MUST Fail. | 6 |
| CD-8 | Global config MUST reject every directive that affects bytes, gates, version or publishing, with "belongs in letsgo.mod". | 10 |
| CD-9 | Precedence MUST be flag > env > global > default. | 8 |
| CD-10 | `plan --explain` MUST name the source of each machine value. | 9 |
| CD-11 | No global value may appear in the manifest (except the go *version*, as today). | 10 |
| CD-12 | `token-command` MUST run only when neither a flag nor env provides a token, and its output MUST NOT be written anywhere. | 11 |
| CD-13 | `proxy` MUST change which proxy is warmed. | 7 |

## Errors and edge cases

- A store entry whose hash doesn't match its path: Fail, naming the path.
  The entry is never run.
- `LETSGO_CONFIG` pointing at a missing file: an error.
  A missing default file is fine.
- A `token-command` that exits non-zero: fall through to "no token", with
  stderr shown.

## Acceptance scenarios

1. **Given** repository A pinning multi `v0.2.0` and repository B pinning `v0.3.0`, both installed, **when** each runs `plan`, **then** both succeed with no reinstall. (CD-2, CD-3)
2. **Given** a byte flipped in a store binary, **when** plan runs, **then** it Fails on the hash. (CD-2)
3. **Given** `build linux/amd64` in `config.mod`, **when** any command runs, **then** there's an error "belongs in letsgo.mod". (CD-8)
4. **Given** `proxy https://p.internal` globally and `GOPROXY` unset, **when** `plan --explain` runs, **then** it shows the proxy and the file it came from. (CD-10, CD-13)
5. **Given** both `letsgo-env.mod` and `.letsgo/env.mod`, **when** plan runs, **then** it Fails. (CD-7)
