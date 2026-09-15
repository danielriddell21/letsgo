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
	"github.com/danielriddell21/letsgo/internal/zig"
)

// cToolchain and cToolchainHost name the two checks that describe the C
// compiler: which one it was, and what it ran on. Both are inputs, and the
// second is one the compiler does not remove.
const (
	cToolchain     = "c toolchain"
	cToolchainHost = "c toolchain host"
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

	cgo, ok := obtainCCompiler(ctx, result, m)
	if !ok {
		return
	}

	moduleDir := filepath.Join(source, filepath.FromSlash(m.ModuleDir))

	commands, err := discover.FindMainPackages(moduleDir, m.Project)
	if err != nil {
		result.add("rebuild", Fail, "%v", err)
		return
	}

	compareRebuilt(ctx, o, result, rebuildInputs{
		m:         m,
		source:    source,
		moduleDir: moduleDir,
		cgo:       cgo,
		sameHost:  sameCCompilerHost(result, m),
		commands:  commands,
	})
}

// rebuildInputs is what a rebuild needs: the release being checked, the tree it
// is checked against, and the compilers that do it.
type rebuildInputs struct {
	m         *manifest.Manifest
	source    string
	moduleDir string
	cgo       zig.Toolchain

	// sameHost says whether cgo artifacts may be held to their digests, which
	// they may only on a host like the one that built them.
	sameHost bool

	commands []discover.MainPackage
}

// hostBound reports whether an artifact's digest depends on the host that
// compiled it, and so cannot be required to match on this one.
func (in rebuildInputs) hostBound(a manifest.Artifact) bool {
	return !in.sameHost && a.Build.Env["CGO_ENABLED"] == "1"
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

// obtainCCompiler fetches the C toolchain the release recorded.
//
// The same compiler, not merely the same version: the manifest pins the
// archive it came from, so a verifier gets the bytes the release was built
// with rather than whatever now carries that version number. Without this a
// cgo release could not be checked at all, which is the whole reason the
// manifest records it.
func obtainCCompiler(
	ctx context.Context,
	result *Result,
	m *manifest.Manifest,
) (zig.Toolchain, bool) {
	cc := m.Builder.CC
	if cc == nil {
		return zig.Toolchain{}, true
	}
	if cc.Name != "zig" {
		result.add(cToolchain, Fail,
			"the release was built with %q, which this letsgo cannot obtain", cc.Name)
		return zig.Toolchain{}, false
	}

	cache, err := os.UserCacheDir()
	if err != nil {
		result.add(cToolchain, Fail, "%v", err)
		return zig.Toolchain{}, false
	}

	toolchain, err := zig.Ensure(ctx, cc.Version, cc.Digest, filepath.Join(cache, "letsgo"))
	if err != nil {
		result.add(cToolchain, Fail, "%v", err)
		return zig.Toolchain{}, false
	}

	result.add(cToolchain, Pass, "zig %s, as recorded", toolchain.Version)
	return toolchain, true
}

// sameCCompilerHost reports whether this machine is the kind of machine the
// release's cgo artifacts were compiled on.
//
// It matters because the C toolchain is pinned but not hermetic: the same zig,
// compiling the same source for the same target, emits different objects on a
// Linux host than on a macOS one. Verified in CI by compiling a bare C file
// with nothing but the pinned compiler and comparing the objects, which differ
// on all three runners.
//
// So a cgo release reproduces byte for byte on a host like the one that built
// it, and elsewhere reproduces only its pure-Go artifacts. Saying so up front
// is the difference between a narrower claim and a broken one.
func sameCCompilerHost(result *Result, m *manifest.Manifest) bool {
	cc := m.Builder.CC
	if cc == nil {
		return true
	}

	local := gobuild.Host().String()
	switch cc.Host {
	case "":
		result.add(cToolchainHost, Warn,
			"the release does not record the host it was built on, so its cgo artifacts cannot be held to a digest here")
		return false
	case local:
		result.add(cToolchainHost, Pass, "%s, as recorded", local)
		return true
	default:
		result.add(cToolchainHost, Warn,
			"built on %s, verifying on %s; zig generates host-dependent code, so cgo artifacts are\n"+
				"reported but not required to match — rebuild on %s to hold them to a digest",
			cc.Host, local, cc.Host)
		return false
	}
}

func compareRebuilt(ctx context.Context, o Options, result *Result, in rebuildInputs) {
	groups, err := rebuildGroups(in.m, in.commands)
	if err != nil {
		result.add("rebuild", Fail, "%v", err)
		return
	}

	var tally comparison
	for _, group := range groups {
		produced, err := build.Run(ctx, build.Options{
			ModuleDir:    in.moduleDir,
			FilesDir:     in.source,
			Commands:     group.commands,
			Name:         group.name,
			Version:      in.m.Version,
			ModTime:      sourceDate(in.m),
			Targets:      group.targets,
			ExtraFiles:   build.FindDocumentation(in.source),
			ExactLDFlags: exactFlags(group.artifacts),
			Tags:         recordedTags(group.artifacts),
			CGo:          in.cgo,
			Toolchain:    in.m.Builder.Go,
			WorkDir:      filepath.Join(o.WorkDir, "rebuild"),
		})
		if err != nil {
			result.add("rebuild", Fail, "%v", err)
			return
		}
		tally.collect(in, produced)
	}
	tally.report(result, in.m)
}

// comparison is what rebuilding established, artifact by artifact.
//
// Differences are kept in two piles rather than one, because they do not mean
// the same thing: a pure-Go artifact that does not reproduce is a failure,
// while a cgo artifact rebuilt on a host unlike the one that built it was never
// claimed to.
type comparison struct {
	matched   int
	problems  []string
	hostBound []string
}

// collect compares one group's rebuilt artifacts against the manifest.
func (c *comparison) collect(in rebuildInputs, produced []build.Artifact) {
	for _, got := range produced {
		want, ok := in.m.Artifact(got.Archive)
		if !ok {
			continue
		}

		var detail string
		switch differing := differingBinaries(got, want); {
		case len(differing) > 0:
			// Reported first: if the binaries differ, nothing downstream of
			// the compiler is worth investigating yet.
			detail = fmt.Sprintf("%s: %s", got.Target, strings.Join(differing, "; "))
		case got.ArchiveSHA256 != want.SHA256:
			detail = fmt.Sprintf("%s: binaries match but the archive does not (%s vs %s)",
				got.Target, short(got.ArchiveSHA256), short(want.SHA256))
		default:
			c.matched++
			continue
		}

		if in.hostBound(want) {
			c.hostBound = append(c.hostBound, detail)
		} else {
			c.problems = append(c.problems, detail)
		}
	}
}

// report turns the tally into checks.
func (c *comparison) report(result *Result, m *manifest.Manifest) {
	if len(c.hostBound) > 0 {
		result.add("cgo rebuild", Warn, "%s", strings.Join(append(c.hostBound,
			"these were compiled on another host, where zig generates different code;",
			"rebuild on that host to hold them to a digest"), "\n"))
	}

	switch {
	case len(c.problems) > 0:
		// A mismatch is usually a real difference, but one cause is neither a
		// defect nor obvious: a repository that does not pin its line endings
		// checks out differently on Windows, so the same commit yields
		// different source and therefore different bytes.
		result.add("rebuild", Fail, "%s", strings.Join(append(c.problems,
			"if this repository has no .gitattributes pinning line endings, a checkout",
			"on Windows will differ from one on Linux and cannot reproduce either"), "\n"))
	case c.matched == 0 && len(c.hostBound) == 0:
		result.add("rebuild", Fail, "nothing was rebuilt; the manifest describes artifacts this source does not produce")
	default:
		result.add("rebuild", Pass, "%d of %d artifacts reproduce byte for byte", c.matched, len(m.Artifacts))
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
