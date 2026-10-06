# letsgo

A release tool for Go projects. It plans a release, builds it, publishes it to the forge and package channels, and can later promote or yank it.

## Language

**Plan**:
The recorded intent for a release: what will be released and how, captured before anything is built or published.
_Avoid_: Dry run, preview

**Release**:
A versioned set of built artefacts produced from a Plan for one tag.
_Avoid_: Build, deployment

**Published Release**:
A Release as it exists on the forge after a Publication: its tag, its assets and its Manifest. What `verify`, `diff`, `audit`, `promote` and `yank` read back. A draft or pre-release is still a Published Release.
_Avoid_: Forge release, remote release

**Publication**:
The act of making a built Release available: the forge release, the checksum and proxy gate, the Homebrew tap formula and the container images. It starts from a built Release and does not include the build.
_Avoid_: Deploy, upload, push

**Promote**:
Publishing an existing pre-release as the current release without rebuilding it.
_Avoid_: Re-release

**Yank**:
Withdrawing a published Release so consumers stop receiving it.
_Avoid_: Delete, unpublish

**Manifest**:
The `letsgo.json` record of what a Release contains, read back by later commands such as Promote and Yank.
_Avoid_: Lockfile

**Config**:
The `letsgo.mod` file where a project states how its releases are made.
_Avoid_: Settings

**Plugin**:
A separate binary, pinned by digest in letsgo.mod, that answers one or more Hooks. It never sees a token, and `verify` never runs it.
_Avoid_: Extension, add-on

**Hook**:
A point in a Release where letsgo asks a Plugin a question and records the answer: `ldflags`, `archive-layout` or `tap-files`.
_Avoid_: Callback, event

**Plugin config**:
A Plugin's own `.letsgo/<name>.mod`, written in the same syntax as the Config and read through the public `modsyntax` package by `plugin.LoadConfig`. A legacy `letsgo-<name>.mod` at the repository root is still read.
_Avoid_: Plugin settings

**Tap**:
The Homebrew repository that holds a project's formula.
_Avoid_: Brew repo

**Stable surface**:
What v1 promises not to break within a major version: the command line, Config, JSON output, Manifest and plan files, the Plugin wire contract and the five public Go packages. Listed in `docs/stability.md`.
_Avoid_: API, public interface

**Gate**:
A check that must pass before a Publication is considered safe, such as the proxy warm-up and checksum database lookup.
_Avoid_: Guard, precondition
