# HLD: Dockerfile images through the `images` hook

Status: proposed.

| | |
|---|---|
| ADRs | [ADR-0027](../adr/0027-images-hook-plugin-builds-core-pushes.md) |
| Plugin | `letsgo-images` (letsgo-plugins) |

## Problem

letsgo builds one image per command, from the binaries, on a base image.
A repository that also ships images from its own Dockerfile, such as Talos
with its `fixer` stage, has nowhere to put them, and pushes them by hand with
different tags and no record.

## Shape

```
letsgo.mod                        .letsgo/images.mod
-----------                       ------------------
image                             timeout 45m
plugin images letsgo-images \
    v0.1.0 sha256:…               image fixer {
                                      dockerfile ./build/fixer.Dockerfile
                                      target     fixer
                                      suffix     -fixer
                                      platform   linux/amd64
                                  }
```

`.letsgo/images.mod` uses letsgo.mod's grammar (ADR-0025).

| setting | | meaning |
| --- | --- | --- |
| `timeout <duration>` | top level, optional | how long core lets the hook run; default `30m`, at most `2h` |
| `image <name> { … }` | repeatable | one image; `<name>` is for messages |
| `dockerfile <path>` | required | relative to the repository root |
| `target <stage>` | optional | the build stage |
| `suffix <text>` | required, unique | appended to every tag |
| `platform <os/arch>…` | optional | defaults to the runner's platform |

## Flow

### Plan

1. With an `images` pin, core reads images.mod for `timeout` only. More than
   `2h`, or not a duration, is a **Fail** at the setting's position.
2. `docker buildx version` must succeed, or it's a **Fail**: "the images hook
   builds with docker buildx, which isn't available here".
3. Each image's platforms that aren't among the release's Linux targets get a
   **Warn**. The image still builds; its binaries just won't be staged.
4. Without an `image` directive, the plan resolves the default reference as
   `image` would and shows that core builds no image of its own.

Plan doesn't run the Plugin. The other settings are the Plugin's to check,
when it runs.

### Release

After the build and archives, before any publication:

1. **Stage.** Core creates `<dist>/images-hook/` and copies each Linux binary
   to `context/<os>_<arch>/<binary>`, preserving the mode.
2. **Run.** Core runs the Plugin, with the repository root as its working
   directory and images.mod's timeout:

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

   The Plugin builds each image with
   `docker buildx build -f <dockerfile> [--target] --platform …
   --build-context letsgo=<context>
   --output type=oci,dest=<output>/<name>.tar,tar=false <repo root>`,
   then answers:

   ```json
   {"images": [{"suffix": "-fixer", "layout": "/…/dist/images-hook/out/fixer"}]}
   ```

   A Dockerfile reads the staged binaries with
   `COPY --from=letsgo linux_amd64/talos /usr/local/bin/`.
3. **Check.** Core rejects the answer if:
   - a layout isn't a directory under `output` after resolving symlinks;
   - a suffix is empty, repeated, or isn't a valid tag fragment
     (`[A-Za-z0-9_.-]`, and the full tag at most 128 characters);
   - a layout has no index.
4. **Load.** `oci.LoadLayout` reads `index.json` and its blobs into the same
   shape `ImageBuild` already pushes. A layout with one manifest is pushed
   as that manifest; one with several is pushed as an index.

### Publication

Each plugin image becomes an `ImageBuild` with core's registry and
repository, and with core's `Tags` and `Floating` each given the suffix.
It's pushed by the same `push.go` path, with the same credential. Floating
tags compare against what `<tag><suffix>` currently points at.

### Manifest

```json
"images": [
  {"reference": "ghcr.io/you/talos", "digest": "sha256:…",
   "tags": ["1.2.3", "1.2", "1", "latest"], "platforms": ["linux/amd64"]},
  {"reference": "ghcr.io/you/talos", "digest": "sha256:…",
   "tags": ["1.2.3-fixer", "1.2-fixer", "1-fixer", "latest-fixer"],
   "platforms": ["linux/amd64"], "source": "plugin"}
]
```

`manifest.Image` gains `Source string \`json:"source,omitempty"\``.
Empty means core built it.

### After the release

| command | plugin images |
| --- | --- |
| `verify` | checked like any image: tag → recorded digest |
| repro | not involved; it rebuilds only core's image from the artifacts |
| `promote` | rebuilds at the final tag (ADR-0009), so the hook runs again |
| `yank` | untouched, as for core's images today |
| `--snapshot` | builds, records, doesn't push |

## Wire types

In `letsgo/plugin`:

```go
HookImages Hook = "images"

type ImagesInput struct {
    Project, Version, Tag, Commit, Date string
    Context  string        `json:"context"`
    Output   string        `json:"output"`
    Binaries []ImageBinary `json:"binaries"`
    ConfigDir string       `json:"config_dir"`
}
type ImageBinary struct{ Name, OS, Arch, Path string }
type ImagesOutput struct{ Images []OutputImage `json:"images"` }
type OutputImage struct{ Suffix, Layout string }
```

`Hooks` becomes `{ldflags, archive-layout, tap-files, images}`. `Known`
gains `letsgo-images`.

## Delivery

1. **letsgo:**
   - the wire types, hook and catalogue entry;
   - `Run` takes a per-call timeout;
   - plan: the timeout read and the docker probe;
   - release: staging, the hook call, the checks, `oci.LoadLayout`, and the
     suffixed `ImageBuild`s;
   - `manifest.Image.Source`;
   - docs.
2. **Release letsgo.**
3. **letsgo-plugins:**
   - `cmd/letsgo-images`;
   - bump the `require`;
   - release.
4. **Talos:**
   - pin the Plugin;
   - add images.mod;
   - bump `LETSGO_VERSION`.

## Out of scope

- Separate repositories per image (`talos-fixer`): suffix only.
- Emulation setup (QEMU). A non-native `platform` needs the runner to have
  it.
- Signing or attestations for plugin images.
