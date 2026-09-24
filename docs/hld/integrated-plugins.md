# HLD: thin plugin mains, plugin-aware core

Status: proposal. Nothing here is implemented.

| | |
|---|---|
| Issue | [#25](https://github.com/danielriddell21/letsgo/issues/25) |
| Epic | [#39](https://github.com/danielriddell21/letsgo/issues/39) |
| PRD | [prd/integrated-plugins.md](../prd/integrated-plugins.md) |
| PBS | [pbs/integrated-plugins.md](../pbs/integrated-plugins.md) |
| ADRs | [ADR-0003](../adr/0003-plugin-logic-in-core-thin-mains.md), [ADR-0004](../adr/0004-core-writes-tap-files.md) |

The idea: letsgo-plugins keeps one `main.go` per plugin and nothing else. The
logic moves into letsgo as importable packages, and letsgo knows which
first-party plugins exist. `letsgo-cask` becomes a little integrated: letsgo
runs it during a release, instead of a workflow running it afterwards.

## Where things are today

| | letsgo-plugins today | core equivalent | drift |
| --- | --- | --- | --- |
| hook wire types | redeclared per plugin (`multi/main.go` `input`/`output`) | `internal/plugin/hooks.go` | hand-kept in step |
| `hook.Main` | `internal/hook` | none (core is the caller) | — |
| manifest reader | `cask/main.go` subset, `Schema != 1` → error | `internal/manifest` | **any schema bump breaks cask** |
| tap writer | `internal/tap` (321 + 481 test lines), "mirrors letsgo's exactly … should collapse onto it if letsgo ever exports that code" | `internal/brew/publish.go`, `publish/github/contents.go` | its own client, its own author, no `--snapshot` recorder |
| download URL | `fmt.Sprintf(".../download/%s/%s", tag, name)` | `github.DownloadURL` uses `url.PathEscape` | **they already disagree** on a tag containing `/` (see the monorepo design) |
| repo description and licence | `--desc`, `--license`, `--homepage` flags | `describeRepo` reads them from the forge | the same facts are entered twice |

letsgo-plugins has no dependencies at all (`go.mod` has no `require`). That
was a choice: a plugin shouldn't need letsgo in order to exist. The cost is the
table above.

Core's side of the choice is written down in `cmd/letsgo/plugin.go`
(`describePin`):

> a core carrying a table of plugin names would be a core that knows about
> particular plugins.

This proposal reverses that on purpose, and limits how far it goes (see
**Limits on core's awareness**).

## Proposal

### 1. letsgo exports what plugins import

Three packages are made public. They are the only new public API in letsgo:

| package | contents | from |
| --- | --- | --- |
| `letsgo/plugin` | wire types for every hook, `Main[In, Out](hook, fn)` | `internal/plugin/hooks.go` + letsgo-plugins `internal/hook` |
| `letsgo/manifest` | `letsgo.json` types, `Read`, a schema-tolerant decode | `internal/manifest` |
| `letsgo/plugins/{multi,env,cask}` | each plugin's logic as a pure function | letsgo-plugins `cmd/*` |

letsgo-plugins becomes:

```go
// cmd/letsgo-multi/main.go
package main

import (
	"github.com/danielriddell21/letsgo/plugin"
	"github.com/danielriddell21/letsgo/plugins/multi"
)

func main() { plugin.Main(plugin.HookArchiveLayout, multi.Layout) }
```

plus `go.mod` requiring letsgo, the release workflow, and `letsgo.mod`.
`internal/hook` and `internal/tap` are deleted, and the tests move with the
logic.

**Why keep separate binaries if the code lives in core?** Pinning. A plugin is
opt-in and pinned by digest, and `verify` never runs it. That stays true only
while the plugin is a separate program. If the code were compiled into letsgo,
it would be a feature, and the answer would be config directives. That's a
legitimate endpoint (see **Alternatives**), but it's not this proposal.

**Cost: public API.** letsgo gates its own releases on apidiff. Once exported,
`plugin`, `manifest` and `plugins/*` can't break without a major version.
Keep the plugin packages to one exported function each, and keep their inputs
as the wire types, which already have a compatibility promise because they are
JSON.

**Cost: pins churn.** A plugin binary's digest now changes whenever the letsgo
code it links changes, even if the plugin's behaviour didn't. That is correct,
because the code did change, but it means more pin bumps. `letsgo plugin list`
already shows mismatches. Dependabot on letsgo-plugins' `go.mod` plus a release
on each bump is the steady state.

### 2. Core knows the first-party plugins

A small catalogue in `internal/plugin`:

```go
var Known = map[string]Info{
	"letsgo-multi": {Hook: HookArchiveLayout, Summary: "one archive per target holding every command"},
	"letsgo-env":   {Hook: HookLDFlags,       Summary: "compile environment values into the binary"},
	"letsgo-cask":  {Hook: HookTapFiles,      Summary: "a Homebrew cask beside the formula"},
}
```

It is used in four places:

- `letsgo plugin install letsgo-cask` prints a complete pin instead of
  `plugin <hook> …` (`describePin`).
- `letsgo plugin list --available` lists what can be installed.
- `plan` suggests a plugin in one line when a situation calls for it: a
  variant with a darwin target and a `brew` tap suggests `letsgo-cask`, and
  eleven commands without a layout plugin suggest `letsgo-multi`.
- The pin line tells `letsgo fmt` which hook a known plugin answers, so a
  pin with the wrong hook becomes a plan **Fail** that names the right one.

### Limits on core's awareness

- **It never runs anything.** The catalogue fills in text. Execution still
  goes through `plugin.Run` with a pin checked against the digest.
- **Third-party plugins work the same way.** A plugin that isn't in the
  catalogue still installs, pins and runs. It just gets `<hook>` in the
  printed pin, as it does today.
- **No plugin gets its own directive.** `letsgo.mod` stays a closed set. A
  plugin's settings go in a `<plugin>.mod` file beside it, as `letsgo-env.mod`
  already does.

### 3. `letsgo-cask` through a new `tap-files` hook

Today a workflow runs `letsgo-cask --repo … --tap … dist/letsgo.json` after
the release. It rebuilds the download URLs, asks for the description and
licence as flags, and writes to the tap through its own client and credential.

The proposal: a new hook where core asks "what else goes in the tap?". The
plugin only renders files; core writes them.

```
plugin tap-files letsgo-cask v0.4.0 sha256:…
```

Input: everything the formula writer already has.

```json
{
  "project": "gambit", "version": "1.2.0", "tag": "v1.2.0",
  "repo": "you/gambit", "tap": "you/homebrew-tap",
  "description": "…", "license": "MIT", "homepage": "https://…",
  "caveats": "…",
  "artifacts": [
    {"archive": "gambit-gui_1.2.0_darwin_arm64.tar.gz", "variant": "gui",
     "os": "darwin", "arch": "arm64", "sha256": "…",
     "url": "https://github.com/you/gambit/releases/download/v1.2.0/…",
     "binaries": ["gambit"]}
  ]
}
```

Output:

```json
{ "files": [ { "path": "Casks/gambit-gui.rb", "content": "…" } ] }
```

Which variant and what token (the cask's name) live in `letsgo-cask.mod`
(`variant gui`, `token gambit`). Everything else comes from the input, so the
flags go away.

Core then:

- **checks the paths**: relative, no `..`, under `Casks/`, and not a path a
  formula is being written to. The plugin decides what's in the tap, not where
  in the repository it may write;
- **writes through `brew.Publish`'s path**: the same conditional write, the
  same `letsgo-champ[bot]` author, the same tap client and token split. The
  plugin never sees a token;
- **rehearses in `--snapshot`**: the recorder shows the cask exactly as it
  shows the formula;
- **skips drafts**, with the same message as the formula;
- **handles yank**: `letsgo yank` already regenerates the formula from the
  previous release's manifest. It re-runs the tap-files plugin against that
  manifest too, so the cask is rolled back instead of left pointing at a
  retracted release. That's a bug today that nobody sees.

The hook runs after the artifacts are built and before publishing. Its answer
depends only on digests and URLs that are already known, so a failing cask
stops the release before anything is public, which matches the rest of `plan`.

**Recording.** The tap-files output doesn't change released bytes, so `verify`
has nothing to replay. Record it anyway, as `tap_files: [{path, sha256}]` in
the manifest: yank uses it to know which files it owns, and a reader can see
what a release put in someone else's repository.

**The standalone CLI stays.** `letsgo-cask dist/letsgo.json` keeps working
(it would be the same `cask.Render` behind a flag parser) for a repository
that publishes its cask elsewhere. It loses `--tap` publishing: that is the
hook's job now, and the tap client it needed is gone.

## Existing hooks

`letsgo-multi` and `letsgo-env` get simpler and change nothing else: same
hooks, same wire format, same pins semantics. The tests (the JSON-to-JSON
ones) move to letsgo next to the logic. letsgo-plugins keeps one smoke test per
binary: run it with a sample stdin and check the hook name is enforced.

## Interaction with the monorepo design

- The URL is built in core, so the escaping disagreement disappears. Whatever
  `DownloadURL` does with `services/api/v1.2.0` is what the cask gets.
- `tag` is in the input, so the cask needs no prefix logic.
- `letsgo-mono check` can use `plugin.Known` and the manifest package instead
  of re-parsing either.

## Alternatives

**A. Status quo: zero-dependency plugins.** Plugins stay independent of
letsgo. The drift in the table above is the price, and it has already produced
a divergence (URL escaping) and a trap (`Schema != 1`).

**B. Built-ins.** Fold multi, env and cask into core as directives
(`archive bundle`, `ldflags env …`, `brew cask gui`). This is simplest for
users: no install, no pin. But it gives up the principle that core's config is
a closed set of things every repository might need, and `letsgo-env` in
particular is exactly the thing core refuses to do (a non-literal ldflag). It
could be right for multi alone.

**C. This proposal.** It keeps plugins opt-in and pinned, removes the
duplication, and gives cask the formula's guarantees. It costs public API and
pin churn.

Recommendation: C. If multi ends up pinned by most repositories that ship more
than one command, reconsider it for B.

## Delivery

Phases, in order. Each is a vertical slice: a thin path through every layer, verified end to end. The behaviour each phase must meet is specified in the [PBS](../pbs/integrated-plugins.md).

1. Export the SDK and manifest (no behaviour change) (user stories 10, 11, 12)
2. Catalogue (user stories 1, 2, 3, 4)
3. Tracer bullet: the `tap-files` hook in core (user stories 5, 6, 9)
4. letsgo-cask onto the hook, plus yank (user stories 7, 8)
5. Move the multi and env logic (user stories 10)

## Open questions

- Should `tap-files` be allowed to write under `Formula/` (a plugin that
  replaces core's formula)? Proposed: no, until someone needs it.
- Should letsgo-plugins stay a separate repository at all, or become
  `letsgo/cmd/letsgo-*` released by the same tag? One repository means one
  version and the pins move together, but every letsgo release would produce
  new plugin digests.
- Is it worth enforcing the catalogue's hook at run time (pinning
  `letsgo-cask` to `ldflags` fails)? Proposed: yes, as a plan Fail; the
  catalogue is the only place that knows.
