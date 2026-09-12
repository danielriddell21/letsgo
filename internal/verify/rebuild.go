package verify

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/publish/github"
)

// rebuild compiles the release again from source and compares the result.
func rebuild(ctx context.Context, o Options, result *Result, release *github.Release, m *manifest.Manifest) error {
	source, from, err := obtainSource(ctx, o, release, m)
	if err != nil {
		result.add("source", Fail, "%v", err)
		return nil
	}
	result.SourceFrom = from
	result.add("source", Pass, "%s", from)

	checkToolchain(ctx, result, m)

	commands, err := discover.FindMainPackages(source, m.Project)
	if err != nil {
		result.add("rebuild", Fail, "%v", err)
		return nil
	}

	compareRebuilt(ctx, o, result, m, source, commands)
	return nil
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
	if err := os.MkdirAll(o.WorkDir, 0o755); err != nil {
		return "", err
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

func compareRebuilt(ctx context.Context, o Options, result *Result, m *manifest.Manifest, source string, commands []discover.MainPackage) {
	var problems []string
	matched := 0

	for _, cmd := range commands {
		name := m.Project
		if len(commands) > 1 {
			name = cmd.BinaryName
		}

		targets, wanted := targetsFor(m, name)
		if len(targets) == 0 {
			continue
		}

		produced, err := build.Run(ctx, build.Options{
			ModuleDir:    source,
			Package:      cmd.RelPath,
			Name:         name,
			Version:      m.Version,
			ModTime:      sourceDate(m),
			Targets:      targets,
			ExtraFiles:   build.FindDocumentation(source),
			ExactLDFlags: exactFlags(wanted),
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
			switch {
			case got.BinarySHA256 != want.BinarySHA256:
				// Reported first: if the binaries differ, nothing downstream
				// of the compiler is worth investigating yet.
				problems = append(problems, fmt.Sprintf("%s: binary rebuilt as %s, published %s",
					got.Target, short(got.BinarySHA256), short(want.BinarySHA256)))
			case got.ArchiveSHA256 != want.SHA256:
				problems = append(problems, fmt.Sprintf(
					"%s: binary matches but the archive does not (%s vs %s)",
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

// targetsFor returns the targets belonging to one command, and the artifacts
// describing them.
func targetsFor(m *manifest.Manifest, name string) ([]gobuild.Target, []manifest.Artifact) {
	var targets []gobuild.Target
	var artifacts []manifest.Artifact

	for _, a := range m.Artifacts {
		if !strings.HasPrefix(a.Name, name+"_"+m.Version+"_") {
			continue
		}
		targets = append(targets, gobuild.Target{OS: a.OS, Arch: a.Arch})
		artifacts = append(artifacts, a)
	}
	return targets, artifacts
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
		if err == io.EOF {
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

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode).Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
}
