# HLD: more than one image

Status: implemented. Re-running a failed image push is not yet supported: a rerun rebuilds the images hook's images with new digests, so it starts from a new tag.

| | |
|---|---|
| ADRs | [ADR-0027](../adr/0027-images-hook-plugin-builds-core-pushes.md) |
| Plugin | `letsgo-docker` (letsgo-plugins) |

## Problem

letsgo builds one image per command, from the binary, on a base image.
Talos needs two images from its own Dockerfile: the release image, and a
`fixer` stage that adds more than the binary. letsgo can express neither.

## Shape

Two ways to add images, which share one tagging rule:

| need | how | reproducible |
| --- | --- | --- |
| the same binary, set up differently | `image <name> ( … )` blocks in letsgo.mod | yes |
| anything a Dockerfile does | the `images` hook, answered by `letsgo-docker` | no; recorded |

**The tagging rule:** every image is pushed to the release's repository under
every tag the release pushes, with its suffix appended (`1.2.3-fixer`,
`1.2-fixer`, `latest-fixer`, `rc-fixer`). Floating tags move forward only, as
they do today. Suffixes are unique across everything a release pushes.

### Core blocks

```
image ghcr.io/you/talos
image base gcr.io/distroless/static
image cmd serve

image fixer (
    suffix -fixer
    cmd    fix
    base   gcr.io/distroless/static
    expose 8080
)
```

| setting | | meaning |
| --- | --- | --- |
| `suffix <text>` | required | `-` or `_`, then a letter; letters, digits, `.`, `-`, `_`, with a letter after every `.` (`plugin.CheckSuffix`), so it can't spell another version's tag (`-rc.1`, `.4`); appended to every tag |
| `base <ref>` | optional | as `image base`; scratch when unset |
| `cmd <arg>…` | optional | as `image cmd` |
| `expose <port>…` | optional | as `image expose` |

- A block inherits nothing from the release image except the repository.
- A block builds every command, on every Linux target, exactly as the release
  image does. A module with several commands pushes each one to
  `<repo>/<binary>:<tag><suffix>`.
- The names `base`, `cmd`, `expose` and `plugin` are reserved, because they
  are already settings of the bare `image` directive.
- A block needs the release image's repository, so it needs an `image`
  directive or the default reference.

### `image plugin`

`image plugin` says the release image comes from the images hook: core builds
no release image of its own. Under it:

- `image base`, `image cmd` and `image expose` are a plan **Fail**, since
  nothing reads them;
- an `images` pin is required;
- core blocks still build.

### letsgo-docker

```
letsgo.mod                         .letsgo/docker.mod (optional)
----------                         -----------------------------
image plugin                       timeout 45m
plugin images letsgo-docker \
    v0.1.0 sha256:…                image release (
                                       target talos
                                   )

                                   image fixer (
                                       dockerfile ./build/fixer.Dockerfile
                                       target     fixer
                                       suffix     -fixer
                                       platform   linux/amd64
                                   )
```

| setting | | meaning |
| --- | --- | --- |
| `timeout <duration>` | top level, optional | read by core; default `30m`, at most `2h` |
| `image <name> ( … )` | repeatable | one image; `<name>` names its layout and its messages |
| `dockerfile <path>` | optional | relative to the repository root; defaults to `Dockerfile` |
| `target <stage>` | optional | the build stage; the last one when unset |
| `suffix <text>` | optional for one image | none means the release image |
| `platform <os/arch>…` | optional | defaults to the runner's platform |

With no docker.mod at all, letsgo-docker builds `./Dockerfile` as the
release image. At most one image may leave out its suffix.

## Rules for an unsuffixed plugin image

| letsgo.mod | core release image | plugin may answer an unsuffixed image |
| --- | --- | --- |
| `image plugin` | not built | required, exactly one |
| no `image` directive | not built | allowed, at most one; pushed to the default reference |
| `image` or `image <ref>` | built | no: the release fails before publishing |

Plan can't know what a Plugin will answer, so the last two rows are checked
when the hook runs. That is still before anything is published.

## Flow

### Plan

1. **Core blocks.** Each is resolved like the release image: its base is
   checked, it is reported as a **Pass**, and duplicate suffixes are a
   **Fail**.
2. **`image plugin`** without an `images` pin is a **Fail**, as is
   `image plugin` beside `base`, `cmd` or `expose`.
3. **With an `images` pin:**
   - the Plugin's config is read for `timeout` and `platform` only. A bad
     timeout is a **Fail** placed in that file.
   - A platform the release doesn't build is a **Warn**.
   - `docker buildx version` must work, or it's a **Fail**. That probe is
     letsgo-docker's need, but buildx is the only builder a first-party
     Plugin uses, so core probes for it when the pinned command is
     `letsgo-docker`. Core and letsgo-docker find docker the same way:
     at the absolute path in `LETSGO_DOCKER`, else in the system
     directories, never through `PATH`.

### Release

After the build and archives, before any publication:

1. **Core images.** The release image, if built, then each block's image.
2. **Stage.** The release's own Linux binaries (not variants) are copied to
   `<dist>/images-hook/context/<os>_<arch>/<binary>`.
3. **Run.** The Plugin runs with its config's timeout, the repository root
   as its working directory, and no forge, registry or GitHub Actions
   (`ACTIONS_ID_TOKEN_REQUEST_*`, `ACTIONS_RUNTIME_TOKEN`) credentials in its
   environment. It runs in its own process group, so a timeout kills
   whatever it started too. Its input:

   ```json
   {
     "project": "talos", "version": "1.2.3", "tag": "v1.2.3",
     "commit": "…", "date": "2026-10-13T12:00:00Z",
     "context": "/…/dist/images-hook/context",
     "output": "/…/dist/images-hook/out",
     "binaries": [{"name": "talos", "os": "linux", "arch": "amd64",
                   "path": "linux_amd64/talos"}],
     "config_dir": "/…/.letsgo"
   }
   ```

   letsgo-docker creates a `docker-container` buildx builder named
   `letsgo-docker` on first use (docker's default driver can export OCI
   only with the containerd image store), then runs, for each image:

   ```
   docker buildx build --builder letsgo-docker --file <dockerfile> [--target <stage>]
     --platform <platforms> --build-context letsgo=<context>
     --output type=oci,tar=false,dest=<output>/<name> <repo root>
   ```

   with `SOURCE_DATE_EPOCH` set to the commit time. It answers:

   ```json
   {"images": [{"suffix": "", "layout": "/…/out/release"},
               {"suffix": "-fixer", "layout": "/…/out/fixer"}]}
   ```

   A Dockerfile reads the staged binaries with
   `COPY --from=letsgo ${TARGETOS}_${TARGETARCH}/talos /usr/local/bin/`.

4. **Check.** The whole answer is checked before any layout is read:
   - every suffix passes `plugin.CheckSuffix`, or is empty;
   - suffixes are unique across the answer and core's own images;
   - the unsuffixed-image rules above hold;
   - no full tag is longer than 128 characters;
   - each layout is a directory under `output` after resolving symlinks.
5. **Load.** `oci.LoadLayout` reads each layout: its image manifests, the
   attestations a builder attaches, and their blobs. Every blob is hashed as
   it is read and checked by digest and size before anything is pushed, and
   only regular files are read, never a symlink. Core builds the index itself
   over those manifests, with the release's annotations, which is what lets a
   floating tag read the version; the names a builder gave the image on its
   own machine (`org.opencontainers.image.ref.name`) are dropped.

### Publication

All images are pushed in the same way, with the same credential and the same
floating-tag comparison. A plugin image's blobs are read from its layout one
at a time as they are pushed.

### Manifest

```json
"images": [
  {"reference": "ghcr.io/you/talos", "digest": "sha256:…",
   "tags": ["1.2.3", "1.2", "1", "latest"], "platforms": ["linux/amd64"],
   "source": "plugin"},
  {"reference": "ghcr.io/you/talos", "digest": "sha256:…",
   "tags": ["1.2.3-fixer", "…"], "platforms": ["linux/amd64"],
   "source": "plugin"}
]
```

`manifest.Image.Source` is `"plugin"` for a plugin image, and empty for one
core built, including a core block.

### After the release

| command | core blocks | plugin images |
| --- | --- | --- |
| `verify` | tag → recorded digest | tag → recorded digest |
| repro | not rebuilt; it rebuilds only the release image's digest | not involved |
| `apply` | held byte for byte | digest may differ; logged |
| `promote` | rebuilt at the final tag | the hook runs again |
| `yank` | untouched | untouched |
| `--snapshot` | built, recorded, not pushed | built, recorded, not pushed |

## Wire types

In `letsgo/plugin`:

```go
HookImages Hook = "images"

type ImagesInput struct {
    Project, Version, Tag, Commit, Date string
    Context, Output string
    Binaries  []ImageBinary
    ConfigDir string
}
type ImageBinary struct{ Name, OS, Arch, Path string }
type ImagesOutput struct{ Images []OutputImage }
type OutputImage struct{ Suffix, Layout string } // Suffix "" = the release image
```

`Known` gains `letsgo-docker`.

## Delivery

1. **letsgo:**
   - the hook, its wire types and the catalogue entry;
   - `RunWithin`;
   - `image <name> ( … )` blocks and `image plugin`;
   - plan checks;
   - `oci.LoadLayout` and `PushLayout`;
   - the release step and the manifest's `source`;
   - `apply`'s exception for plugin image digests;
   - docs.
2. **Release letsgo.**
3. **letsgo-plugins:**
   - `cmd/letsgo-docker`;
   - bump the `require`;
   - release.
4. **Talos:**
   - pin letsgo-docker;
   - `image plugin`;
   - a docker.mod naming the release target and `fixer`;
   - bump `LETSGO_VERSION`.

## Out of scope

- Separate repositories per image (`talos-fixer`): suffix only.
- Emulation setup (QEMU). A non-native `platform` needs the runner to have
  it.
- Signing or attestations of letsgo's own for plugin images.
