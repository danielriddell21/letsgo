# ADR-0027: Dockerfile images come from an `images` hook; the Plugin builds, core pushes

- Status: proposed
- Date: 2026-10-13
- HLD: [hld/images-hook.md](../hld/images-hook.md)

## Context

letsgo publishes one image per command, assembled from the built binaries
on a base image (`internal/oci`). It is reproducible, and `verify` checks
that each tag still points at the recorded digest.

Some repositories need more than that. Talos ships its service and a second
`fixer` image from a stage of its own Dockerfile. letsgo can't express that:

- a second `image` directive fails;
- there's no way to build from a Dockerfile.

A Dockerfile build runs arbitrary steps and isn't reproducible. letsgo's
rule is that a Plugin may change a released byte only if the change is
recorded, and that a Plugin never sees a token.

## Decision

- Add a fourth hook, `images`, run after the build and before anything is
  published.
- First-party Plugin `letsgo-images` (letsgo-plugins) answers it: it runs
  `docker buildx build` for each image in `.letsgo/images.mod` and writes an
  OCI layout into a scratch directory core hands it.
- Core stages the release's Linux binaries into the build context first, so
  a Dockerfile can `COPY` the released bytes rather than rebuild them.
- The Plugin returns `{suffix, layout}` per image. Core checks that each
  layout path is inside the scratch directory, loads the layout and pushes it
  with its own credential. The Plugin never sees a token.
- Each image goes to the same repository as core's image, under every tag
  core computes, with the suffix appended (`1.2.3-fixer`, `latest-fixer`).
  Floating tags follow the same newer-than rules.
- The manifest records each one in `images` with `source: "plugin"`.
  `verify` already checks tag → digest without rebuilding, so it covers them
  unchanged. Repro rebuilds only core's own image, so it never sees them.
- Core reads `timeout` from images.mod: default 30m, above 2h is a plan Fail.
  Every other hook keeps the one-minute limit.
- When the hook is pinned, plan fails if `docker buildx version` fails.
- Without an `image` directive, plugin images go to the default reference
  (`ghcr.io/<owner>/<repo>`) and core builds no image of its own. With one,
  they're published alongside it.

## Consequences

- A repository can publish Dockerfile images with letsgo's tags, credentials
  and manifest record, without a separate workflow step.
- These images are recorded, not reproducible. `verify` proves the tag still
  points at what was published, not what it was built from.
- `images` is the first hook whose answer is a file core publishes rather
  than JSON it records, so the path checks are the boundary.
- Core needs docker only when the hook is pinned.
- The hook and its wire types fall under letsgo's apidiff gate.

## Alternatives considered

- **Core directives** (repeated `image` blocks with `dockerfile`, `target`,
  `suffix`, `platform`). Rejected: it puts non-reproducible builds and a
  docker dependency into core for every repository, when only a few want it.
- **Plugin declares specs, core builds with `internal/oci`.** Rejected: it
  stays reproducible but can't run a Dockerfile, which is the actual need.
- **Plugin pushes.** Rejected: a Plugin never sees a token.
- **Timeout on the pin line, or a fixed limit.** Rejected by choice: the
  build's own config is where its length is known. Core reads it from there
  and caps it, so a Plugin still can't run unbounded.
