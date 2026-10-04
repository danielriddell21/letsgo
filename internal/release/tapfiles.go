package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/plugin"
	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/manifest"
)

// applyTapFilesPlugin asks the tap-files plugin what else belongs in the
// Homebrew tap: a cask, most often, beside the formula core writes on its
// own.
//
// It only renders and validates. Writing is a call to the forge, and happens
// later, in the same tap update as the formula; nothing here needs that to
// have happened yet, because every digest and URL a plugin could ask for is
// already fixed once the artifacts above are built. That is also why a
// failing or misbehaving plugin stops the release here, before anything —
// the manifest included — is even written to disk.
//
// info is the repository's description, licence and homepage, already read
// from the forge by the caller — nil when there is none to read, which
// leaves those fields empty rather than failing the release over them.
func applyTapFilesPlugin(ctx context.Context, p *plan.Plan, artifacts []build.Artifact, info *github.RepoInfo) ([]plugin.TapFile, error) {
	configured, ok := p.Plugins[plugin.HookTapFiles]
	if !ok {
		return nil, nil
	}
	if p.Tap == (github.Repo{}) {
		return nil, fmt.Errorf("release: %s answers tap-files, but no Homebrew tap is configured (`brew` directive)",
			configured.Command)
	}
	if !p.HasRepo || p.Repo.Host != "github.com" || p.Tag == "" {
		return nil, fmt.Errorf("release: %s needs a GitHub repository and a tag to build tap URLs", configured.Command)
	}

	in := tapFilesInput(p, artifacts, info)
	return RunTapFiles(ctx, configured, p.RootDir, p.PluginsDir(), in)
}

// RunTapFiles runs a pinned tap-files plugin and validates its answer.
//
// Shared by a release building fresh artifacts and by yank, which rebuilds
// the same input from a previous release's manifest: both need the same
// execution and the same path checks.
func RunTapFiles(ctx context.Context, configured plugin.Plugin, rootDir, pluginsDir string, in plugin.TapFilesInput) ([]plugin.TapFile, error) {
	in.ConfigDir = filepath.Join(rootDir, ".letsgo")

	var out plugin.TapFilesOutput
	if err := plugin.Run(ctx, configured, rootDir, pluginsDir, in, &out); err != nil {
		return nil, err
	}
	if err := validateTapFiles(out.Files); err != nil {
		return nil, fmt.Errorf("plugin %s: %w", configured.Command, err)
	}
	return out.Files, nil
}

// tapFilesInput assembles the tap-files hook's input from what the release
// already knows: the same facts a Homebrew formula is written from.
func tapFilesInput(p *plan.Plan, artifacts []build.Artifact, info *github.RepoInfo) plugin.TapFilesInput {
	repo := github.Repo{Owner: p.Repo.Owner, Name: p.Repo.Name}

	in := plugin.TapFilesInput{
		Project:   p.Project,
		Version:   p.Version,
		Tag:       p.Tag,
		Repo:      repo.String(),
		Tap:       p.Tap.String(),
		Homepage:  "https://github.com/" + repo.String(),
		Caveats:   p.BrewCaveats(),
		Artifacts: make([]plugin.TapArtifact, 0, len(artifacts)),
	}
	if info != nil {
		in.Description, in.License = info.Description, info.License
		if info.Homepage != "" {
			in.Homepage = info.Homepage
		}
	}

	for _, a := range artifacts {
		binaries := make([]string, 0, len(a.Binaries))
		for _, b := range a.Binaries {
			binaries = append(binaries, b.Name)
		}
		in.Artifacts = append(in.Artifacts, plugin.TapArtifact{
			Archive:  a.Archive,
			Variant:  groupFor(p, a).Variant,
			OS:       a.OS,
			Arch:     a.Arch,
			SHA256:   a.ArchiveSHA256,
			URL:      github.DownloadURL(repo, p.Tag, a.Archive),
			Binaries: binaries,
		})
	}
	return in
}

// validateTapFiles checks what a tap-files plugin answered before anything is
// written: the plugin decides what goes in the tap, not where in the
// repository it may write.
func validateTapFiles(files []plugin.TapFile) error {
	seen := make(map[string]bool, len(files))
	for _, f := range files {
		if !validTapPath(f.Path) {
			return fmt.Errorf("%q is not a valid tap path: it must be relative, contain no \"..\", "+
				"and stay under Casks/", f.Path)
		}
		if seen[f.Path] {
			return fmt.Errorf("%q was returned more than once", f.Path)
		}
		seen[f.Path] = true
	}
	return nil
}

// tapFileRecords is what the manifest keeps of a tap-files plugin's answer:
// not the released bytes — nothing here changes those — but enough for a
// reader to see what a release put in someone else's repository, and for
// yank to know which files it owns.
func tapFileRecords(files []plugin.TapFile) []manifest.TapFile {
	if len(files) == 0 {
		return nil
	}
	out := make([]manifest.TapFile, 0, len(files))
	for _, f := range files {
		sum := sha256.Sum256([]byte(f.Content))
		out = append(out, manifest.TapFile{Path: f.Path, SHA256: hex.EncodeToString(sum[:])})
	}
	return out
}

// validTapPath reports whether p is a path a tap-files plugin may write to:
// relative, already clean, and under Casks/. Confining it to that one
// directory is also what keeps it away from Formula/, where core writes the
// formula itself.
func validTapPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsRune(p, '\\') {
		return false
	}
	if path.Clean(p) != p {
		return false
	}
	dir, _, ok := strings.Cut(p, "/")
	return ok && dir == "Casks"
}
