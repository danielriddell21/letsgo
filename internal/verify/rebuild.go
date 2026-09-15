package verify

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/bytesize"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/publish/github"
)

// maxSourceFile bounds one entry extracted from a source archive. The archive
// is input, and a compressed entry that expands without limit would otherwise
// be answered by filling the disk.
const maxSourceFile = 256 << 20

// rebuild compiles the release again from source and compares the result.
// Nothing here returns an error: a rebuild that cannot proceed is a failed
// check, not a failed verification. Reporting it as an error would abandon the
// checks already gathered, which are the reason anyone ran this.
func rebuild(ctx context.Context, o Options, result *Result, release *github.Release, m *manifest.Manifest) {
	source, from, err := obtainSource(ctx, o, release, m)
	if err != nil {
		result.add("source", Fail, "%v", err)
		return
	}
	result.SourceFrom = from
	result.add("source", Pass, "%s", from)

	checkToolchain(ctx, result, m)

	moduleDir := filepath.Join(source, filepath.FromSlash(m.ModuleDir))

	commands, err := discover.FindMainPackages(moduleDir, m.Project)
	if err != nil {
		result.add("rebuild", Fail, "%v", err)
		return
	}

	compareRebuilt(ctx, o, result, m, source, moduleDir, commands)
}

// obtainSource produces a tree to rebuild from.
//
// A local checkout is preferred, and the distinction matters to what the
// result means: rebuilding from the repository ties the binaries to source
// anyone can review, while rebuilding from the release's own source archive
// only shows the release is internally consistent.
func obtainSource(ctx context.Context, o Options, release *github.Release, m *manifest.Manifest) (dir, from string, err error) {
	if o.Dir != "" {
		if worktree, err := checkoutCommit(ctx, o, m.Commit); err == nil {
			return worktree, fmt.Sprintf("local checkout at %s", shortCommit(m.Commit)), nil
		}
	}

	if m.Source == nil {
		return "", "", fmt.Errorf(
			"no local checkout of %s and the release published no source archive",
			shortCommit(m.Commit))
	}

	dir = filepath.Join(o.WorkDir, "source")
	if err := extractSource(ctx, o, release, m, dir); err != nil {
		return "", "", err
	}
	// The archive wraps everything in one directory.
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		return "", "", fmt.Errorf("source archive %s does not contain a single root directory", m.Source.Archive)
	}
	return filepath.Join(dir, entries[0].Name()),
		fmt.Sprintf("the release's own source archive (%s)", m.Source.Archive), nil
}

func checkoutCommit(ctx context.Context, o Options, commit string) (string, error) {
	dir := filepath.Join(o.WorkDir, "checkout")
	if err := os.MkdirAll(o.WorkDir, 0o750); err != nil {
		return "", fmt.Errorf("verify: %w", err)
	}
	// Removed first so a re-run is not blocked by a previous attempt.
	_ = os.RemoveAll(dir)
	return dir, discover.AddWorktree(ctx, o.Dir, dir, commit)
}

func extractSource(ctx context.Context, o Options, release *github.Release, m *manifest.Manifest, dir string) error {
	var data []byte
	for _, asset := range release.Assets {
		if asset.Name == m.Source.Archive {
			var err error
			if data, err = o.Client.DownloadAsset(ctx, o.Repo, asset.ID); err != nil {
				return err
			}
			break
		}
	}
	if data == nil {
		return fmt.Errorf("source archive %s is not attached to the release", m.Source.Archive)
	}

	if got := sha256Bytes(data); got != m.Source.SHA256 {
		return fmt.Errorf("source archive digest is %s, manifest says %s", short(got), short(m.Source.SHA256))
	}
	return untar(data, dir)
}

func checkToolchain(ctx context.Context, result *Result, m *manifest.Manifest) {
	local, err := gobuild.Version(ctx, "")
	if err != nil {
		result.add("toolchain", Warn, "%v", err)
		return
	}
	if local == m.Builder.Go {
		result.add("toolchain", Pass, "%s, as recorded", local)
		return
	}
	// Not a failure: Go fetches the recorded toolchain when asked for it, so
	// the rebuild still uses the right compiler.
	result.add("toolchain", Warn, "local %s differs from the recorded %s; building with the recorded one",
		local, m.Builder.Go)
}

func compareRebuilt(
	ctx context.Context,
	o Options,
	result *Result,
	m *manifest.Manifest,
	source, moduleDir string,
	commands []discover.MainPackage,
) {
	groups, err := rebuildGroups(m, commands)
	if err != nil {
		result.add("rebuild", Fail, "%v", err)
		return
	}

	var problems []string
	matched := 0

	for _, group := range groups {
		produced, err := build.Run(ctx, build.Options{
			ModuleDir:    moduleDir,
			FilesDir:     source,
			Commands:     group.commands,
			Name:         group.name,
			Version:      m.Version,
			ModTime:      sourceDate(m),
			Targets:      group.targets,
			ExtraFiles:   build.FindDocumentation(source),
			ExactLDFlags: exactFlags(group.artifacts),
			Tags:         recordedTags(group.artifacts),
			Toolchain:    m.Builder.Go,
			WorkDir:      filepath.Join(o.WorkDir, "rebuild"),
		})
		if err != nil {
			result.add("rebuild", Fail, "%v", err)
			return
		}

		for _, got := range produced {
			want, ok := m.Artifact(got.Archive)
			if !ok {
				continue
			}
			switch differing := differingBinaries(got, want); {
			case len(differing) > 0:
				// Reported first: if the binaries differ, nothing downstream
				// of the compiler is worth investigating yet.
				problems = append(problems,
					fmt.Sprintf("%s: %s", got.Target, strings.Join(differing, "; ")))
			case got.ArchiveSHA256 != want.SHA256:
				problems = append(problems, fmt.Sprintf(
					"%s: binaries match but the archive does not (%s vs %s)",
					got.Target, short(got.ArchiveSHA256), short(want.SHA256)))
			default:
				matched++
			}
		}
	}

	switch {
	case len(problems) > 0:
		// A mismatch is usually a real difference, but one cause is neither a
		// defect nor obvious: a repository that does not pin its line endings
		// checks out differently on Windows, so the same commit yields
		// different source and therefore different bytes.
		problems = append(problems,
			"if this repository has no .gitattributes pinning line endings, a checkout",
			"on Windows will differ from one on Linux and cannot reproduce either")
		result.add("rebuild", Fail, "%s", strings.Join(problems, "\n"))
	case matched == 0:
		result.add("rebuild", Fail, "nothing was rebuilt; the manifest describes artifacts this source does not produce")
	default:
		result.add("rebuild", Pass, "%d of %d artifacts reproduce byte for byte", matched, len(m.Artifacts))
	}
}

// differingBinaries names every executable whose digest does not match what the
// release published. An archive can hold several, and saying which one moved is
// the difference between a lead and a shrug.
func differingBinaries(got build.Artifact, want manifest.Artifact) []string {
	published := make(map[string]string, len(want.Executables()))
	for _, b := range want.Executables() {
		published[b.Name] = b.SHA256
	}

	var differing []string
	for _, b := range got.Binaries {
		switch recorded, ok := published[b.Name]; {
		case !ok:
			differing = append(differing, fmt.Sprintf("%s was rebuilt but the release does not list it", b.Name))
		case recorded != b.SHA256:
			differing = append(differing, fmt.Sprintf("%s rebuilt as %s, published %s",
				b.Name, short(b.SHA256), short(recorded)))
		}
	}
	return differing
}

// rebuildGroup is one archive to rebuild: which binaries go in it, and every
// target it was published for.
type rebuildGroup struct {
	name      string
	commands  []build.Command
	targets   []gobuild.Target
	artifacts []manifest.Artifact
}

// rebuildGroups reconstructs the archives from the manifest rather than from
// the source tree.
//
// The manifest is the authority here: it says which binaries shared an archive,
// and re-deriving that from the commands would mean re-running whatever decided
// the layout — which may have been a plugin this machine does not have.
func rebuildGroups(m *manifest.Manifest, commands []discover.MainPackage) ([]rebuildGroup, error) {
	byName := map[string]string{}
	for _, c := range commands {
		byName[c.BinaryName] = c.RelPath
	}

	var order []string
	groups := map[string]*rebuildGroup{}

	for _, a := range m.Artifacts {
		base := a.BaseName(m.Version)
		if base == "" {
			continue
		}

		group, ok := groups[base]
		if !ok {
			binaries := a.BinaryNames()
			built, err := commandsFor(binaries, commands, byName)
			if err != nil {
				return nil, err
			}
			group = &rebuildGroup{name: base, commands: built}
			groups[base], order = group, append(order, base)
		}

		group.targets = append(group.targets, gobuild.Target{OS: a.OS, Arch: a.Arch})
		group.artifacts = append(group.artifacts, a)
	}

	out := make([]rebuildGroup, 0, len(order))
	for _, name := range order {
		out = append(out, *groups[name])
	}
	return out, nil
}

// commandsFor maps the binaries an archive held back to the packages that
// build them.
func commandsFor(
	binaries []string,
	commands []discover.MainPackage,
	byName map[string]string,
) ([]build.Command, error) {
	// A module with one command names its binary after the project, not after
	// the command's directory, so there is nothing to match on.
	if len(commands) == 1 && len(binaries) == 1 {
		return []build.Command{{Package: commands[0].RelPath, Binary: binaries[0]}}, nil
	}

	out := make([]build.Command, 0, len(binaries))
	for _, binary := range binaries {
		pkg, ok := byName[binary]
		if !ok {
			return nil, fmt.Errorf("the release published %s, which this source has no main package for", binary)
		}
		out = append(out, build.Command{Package: pkg, Binary: binary})
	}
	return out, nil
}

// exactFlags recovers the linker flags a release recorded.
//
// Reconstructing them from the version and commit would usually agree and
// occasionally not — the commit was abbreviated by whichever git produced it,
// and an abbreviation is not derivable from the full hash. Replaying what was
// written down removes the guess.
func exactFlags(artifacts []manifest.Artifact) []string {
	if len(artifacts) == 0 {
		return nil
	}

	fields := strings.Fields(artifacts[0].Build.LDFlags)
	var flags []string
	for i, f := range fields {
		// "-s" and "-w" are re-added by the builder; only the injections
		// need replaying.
		if f == "-X" && i+1 < len(fields) {
			flags = append(flags, "-X", fields[i+1])
		}
	}
	return flags
}

// recordedTags replays the build tags an artifact was compiled with. Tags
// select which files compile, so a rebuild that drops them is not rebuilding
// the same program.
func recordedTags(artifacts []manifest.Artifact) []string {
	if len(artifacts) == 0 {
		return nil
	}
	for _, flag := range artifacts[0].Build.Flags {
		if rest, ok := strings.CutPrefix(flag, "-tags="); ok && rest != "" {
			return strings.Split(rest, ",")
		}
	}
	return nil
}

func sourceDate(m *manifest.Manifest) time.Time {
	return time.Unix(m.SourceDateEpoch, 0).UTC()
}

func sha256Bytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func shortCommit(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

// untar extracts a gzipped tar into dir.
func untar(data []byte, dir string) error {
	zr, err := gzip.NewReader(strings.NewReader(string(data)))
	if err != nil {
		return fmt.Errorf("verify: reading source archive: %w", err)
	}
	tr := tar.NewReader(zr)

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("verify: reading source archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		// An archive entry naming a path outside the destination would write
		// wherever it pleased. Ours cannot, but an archive is input.
		target := filepath.Join(dir, filepath.FromSlash(hdr.Name))
		if !strings.HasPrefix(target, filepath.Clean(dir)+string(os.PathSeparator)) {
			return fmt.Errorf("verify: source archive entry %q escapes the destination", hdr.Name)
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return fmt.Errorf("verify: %w", err)
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode).Perm())
		if err != nil {
			return fmt.Errorf("verify: %w", err)
		}
		// Bounded: the archive is input, and an entry that claims to be
		// small and decompresses forever would otherwise fill the disk.
		written, err := io.Copy(f, io.LimitReader(tr, maxSourceFile))
		if err != nil {
			_ = f.Close()
			return fmt.Errorf("verify: extracting %q: %w", hdr.Name, err)
		}
		if written == maxSourceFile {
			_ = f.Close()
			return fmt.Errorf("verify: source archive entry %q is larger than %s",
				hdr.Name, bytesize.Size(maxSourceFile))
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("verify: extracting %q: %w", hdr.Name, err)
		}
	}
}
