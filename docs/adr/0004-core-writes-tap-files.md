# ADR-0004: Core writes tap files; `tap-files` plugins only render

- Status: proposed
- Date: 2026-09-24
- Issue: [#25](https://github.com/danielriddell21/letsgo/issues/25)
- HLD: [hld/integrated-plugins.md](../hld/integrated-plugins.md)

## Context

letsgo-cask runs in a workflow after the release. It rebuilds the download
URLs, takes the description and licence as flags, and writes to the tap with
its own client and credential. `yank` rolls back the formula but not the
cask.

## Decision

Add a `tap-files` hook. It receives everything the formula writer has, and
returns `{files: [{path, content}]}`. Core then:

- validates the paths: relative, under `Casks/`, and not a formula path;
- writes them through `brew.Publish`, with the same author, conditional write
  and token split;
- rehearses the write under `--snapshot`;
- skips the write for drafts;
- records `tap_files` in the manifest;
- re-renders the files on `yank`.

## Consequences

- A plugin never sees a token.
- A failing cask stops the release before anything is public.
- Yank rolls casks back.
- The standalone `letsgo-cask` keeps rendering, but loses its `--tap*`
  publishing flags.

## Alternatives considered

- **Keep the post-release workflow step.** Rejected: that path has a second
  client and credential, no snapshot, and no yank.
- **Allow writes under `Formula/`.** Rejected until someone needs it.
