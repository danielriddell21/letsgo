# letsgo

A release tool for Go projects. It plans a release, builds it, publishes it to the forge and package channels, and can later promote or yank it.

## Language

**Plan**:
The recorded intent for a release: what will be released and how, captured before anything is built or published.
_Avoid_: Dry run, preview

**Release**:
A versioned set of built artefacts produced from a Plan for one tag.
_Avoid_: Build, deployment

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

**Tap**:
The Homebrew repository that holds a project's formula.
_Avoid_: Brew repo

**Gate**:
A check that must pass before a Publication is considered safe, such as the proxy warm-up and checksum database lookup.
_Avoid_: Guard, precondition
