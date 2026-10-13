# ADR-0027: More than one image: suffixed `image` blocks in core, Dockerfiles through an `images` hook

- Status: accepted
- Date: 2026-10-13
- HLD: [hld/images-hook.md](../hld/images-hook.md)

## Context

letsgo publishes one image per command, assembled from the built binaries
on a base image (`internal/oci`). It needs no Dockerfile and no daemon, it is
reproducible, and `verify` checks that each tag still points at the recorded
digest.

Talos needs more than that:

- its release image is built from its own Dockerfile;
- it ships a second image, `fixer`, from another stage of the same
  Dockerfile, and that stage adds more than the binary.

letsgo can't express either: a second `image` directive fails, and there's
no way to build from a Dockerfile.

Two needs are mixed up here, and they have different answers:

- **The same binary, set up differently** (another command, base or port).
  This is still a function of the binary, so core can build it
  reproducibly.
- **Anything a Dockerfile does** (packages, files, a shell). This runs
  arbitrary steps and isn't reproducible. A Plugin may change a released
  byte only if it is recorded, and a Plugin never sees a token.

OCI standardises the image format and the registry protocol, not a build
file. A Dockerfile, or Containerfile, is one way to produce an OCI image
among several (ko, nix, buildpacks).

## Decision

### Core: suffixed `image` blocks

- `image <name> ( suffix … base … cmd … expose … )` adds another
  reproducible image of the same binaries.
  - `suffix` is required. It is appended to every tag the release pushes
    (`1.2.3-fixer`, `latest-fixer`).
  - A block inherits nothing from the release image except the repository.
  - The names `base`, `cmd`, `expose` and `plugin` are reserved.
- Suffixes are unique across everything a release pushes.

### Core: an `images` hook

- A fourth hook, `images`, runs after the build and before anything is
  published. Its contract is OCI, not docker:
  - core stages the release's Linux binaries into a build context and hands
    the Plugin a scratch directory;
  - the Plugin answers `{suffix, layout}` per image, where `layout` is an OCI
    image layout it wrote there.
- Core checks every answer and every layout path, then pushes each image with
  its own credential under the release's tags plus the suffix. The Plugin
  never sees a token.
- An empty suffix makes that image the release image itself:
  - with `image plugin` in letsgo.mod, core builds no release image of its
    own, and the hook must return exactly one image with no suffix;
  - with no `image` directive at all, core builds none either, and one
    unsuffixed image is allowed;
  - otherwise an unsuffixed image fails the release, before anything is
    published.
- The manifest records plugin images with `source: "plugin"`. `verify`
  already checks tag → digest without rebuilding, so it covers them.
- Core reads `timeout` from the Plugin's config: default 30m, more than 2h is
  a plan Fail. Every other hook keeps its one-minute limit.

### First-party Plugin: `letsgo-docker`

- It lives in letsgo-plugins and answers `images` with `docker buildx`.
  Plan fails early if buildx isn't there.
- With no `.letsgo/docker.mod` it builds `./Dockerfile` as the release
  image. `image <name> ( dockerfile … target … suffix … platform … )`
  blocks add more, and at most one may leave out its suffix.

## Consequences

- Talos publishes both images, its release image and `fixer`, with letsgo's
  tags, credentials and manifest record, and no separate workflow step.
- The same binary under another command needs no docker at all.
- The hook is named for the question, and `letsgo-docker` for how it is
  answered. A `letsgo-ko` or `letsgo-nix` could answer the same hook.
- Plugin images are recorded, not reproducible. `verify` proves a tag still
  points at what was published, not what it was built from.
- `apply` holds a rebuild to its plan byte for byte. A plugin image's digest
  is the one exception: it can't reproduce, so a difference there alone is
  accepted and logged.
- Core needs docker only when `letsgo-docker` is pinned.
- The hook and its wire types fall under letsgo's apidiff gate.

## Alternatives considered

- **Dockerfile settings in core** (repeated `image` blocks with
  `dockerfile`, `target`, `platform`). Rejected: it puts non-reproducible
  builds and a docker dependency into core for every repository.
- **Only the hook, with no core blocks.** Rejected: an image that differs
  only by its command would need docker for no reason.
- **Only core blocks.** Rejected: they can't run a Dockerfile, which Talos
  needs.
- **A Plugin named after the hook (`letsgo-images`).** Rejected: plugins are
  named for what they do (`letsgo-env`, `letsgo-cask`), and the hook is
  deliberately not docker-specific.
- **The Plugin pushes.** Rejected: a Plugin never sees a token.
- **Inferring that the release image comes from the Plugin.** Rejected:
  core would have to read the Plugin's config to know. `image plugin` says it
  in letsgo.mod, where plan can check it.
