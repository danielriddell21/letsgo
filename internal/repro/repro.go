// Package repro produces release artifacts and reports their digests.
//
// It exists so that the reproducibility claim can be tested rather than
// asserted. The same inputs must yield the same digests on any machine, and
// everything in this repository that matters downstream — verification,
// resumable uploads, the release manifest — depends on that being true.
//
// Digests are reported separately for the raw binary and for the archive that
// wraps it. When a comparison fails, that distinction immediately says whether
// the compiler or the archive writer is at fault, which is the difference
// between a hard problem and an easy one.
package repro

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/danielriddell21/letsgo/internal/archive"
	"github.com/danielriddell21/letsgo/internal/gobuild"
)

// Options describes a set of artifacts to produce.
type Options struct {
	// ModuleDir is the module to build.
	ModuleDir string

	// Package is the package to build, e.g. "." or "./cmd/foo".
	Package string

	// Name is the project name, used for the binary and archive filenames.
	Name string

	// Version and Commit are injected through the linker.
	Version string
	Commit  string

	// ModTime is applied to every archive entry. In a real release this is the
	// commit timestamp; it must never come from the clock.
	ModTime time.Time

	// Targets to build. Defaults to DefaultTargets.
	Targets []gobuild.Target

	// ExtraFiles are paths relative to ModuleDir to include in each archive.
	ExtraFiles []string

	// WorkDir is scratch space for binaries and archives. Required.
	WorkDir string

	// GoBin overrides the toolchain binary.
	GoBin string
}

// DefaultTargets covers both archive formats and both executable conventions,
// which is the minimum that meaningfully exercises the pipeline.
var DefaultTargets = []gobuild.Target{
	{OS: "linux", Arch: "amd64"},
	{OS: "darwin", Arch: "arm64"},
	{OS: "windows", Arch: "amd64"},
}

// Artifact is one built and packaged target.
type Artifact struct {
	// Archive is the archive filename, e.g. "foo_1.0.0_linux_amd64.tar.gz".
	Archive string `json:"archive"`

	// Target is the GOOS/GOARCH pair, e.g. "linux/amd64".
	Target string `json:"target"`

	// BinarySHA256 is the digest of the compiled binary before archiving.
	BinarySHA256 string `json:"binary_sha256"`

	// ArchiveSHA256 is the digest of the archive.
	ArchiveSHA256 string `json:"archive_sha256"`
}

// Build compiles and packages every target, returning the artifacts sorted by
// archive name so that two invocations are directly comparable.
func Build(ctx context.Context, o Options) ([]Artifact, error) {
	if o.WorkDir == "" {
		return nil, fmt.Errorf("repro: WorkDir is required")
	}
	if o.ModuleDir == "" {
		return nil, fmt.Errorf("repro: ModuleDir is required")
	}
	if o.Package == "" {
		o.Package = "."
	}
	if o.Name == "" {
		o.Name = "app"
	}
	targets := o.Targets
	if len(targets) == 0 {
		targets = DefaultTargets
	}

	if err := os.MkdirAll(o.WorkDir, 0o755); err != nil {
		return nil, fmt.Errorf("repro: creating work dir: %w", err)
	}

	// The date is part of the linker input, so it has to be derived from
	// ModTime rather than read from the clock. This is the single most common
	// way a release pipeline quietly stops being reproducible.
	ldflags := []string{
		"-X", "main.version=" + o.Version,
		"-X", "main.commit=" + o.Commit,
		"-X", "main.date=" + o.ModTime.UTC().Format(time.RFC3339),
	}

	out := make([]Artifact, 0, len(targets))

	for _, target := range targets {
		binName := o.Name + target.Ext()
		binPath := filepath.Join(o.WorkDir, fmt.Sprintf("%s_%s_%s", o.Name, target.OS, target.Arch), binName)

		if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
			return nil, fmt.Errorf("repro: %w", err)
		}

		err := gobuild.Build(ctx, gobuild.Request{
			Dir:     o.ModuleDir,
			Package: o.Package,
			Output:  binPath,
			Target:  target,
			LDFlags: ldflags,
			GoBin:   o.GoBin,
		})
		if err != nil {
			return nil, err
		}

		binSum, err := sha256File(binPath)
		if err != nil {
			return nil, err
		}

		format := archive.FormatTarGz
		if target.OS == "windows" {
			format = archive.FormatZip
		}

		entries, err := entriesFor(binName, binPath, o.ModuleDir, o.ExtraFiles)
		if err != nil {
			return nil, err
		}

		archiveName := fmt.Sprintf("%s_%s_%s_%s%s", o.Name, o.Version, target.OS, target.Arch, format.Ext())
		archivePath := filepath.Join(o.WorkDir, archiveName)

		if err := writeArchive(archivePath, format, entries, o.ModTime); err != nil {
			return nil, err
		}

		archiveSum, err := sha256File(archivePath)
		if err != nil {
			return nil, err
		}

		out = append(out, Artifact{
			Archive:       archiveName,
			Target:        target.String(),
			BinarySHA256:  binSum,
			ArchiveSHA256: archiveSum,
		})
	}

	slices.SortFunc(out, func(a, b Artifact) int { return strings.Compare(a.Archive, b.Archive) })
	return out, nil
}

func entriesFor(binName, binPath, moduleDir string, extra []string) ([]archive.Entry, error) {
	entries := make([]archive.Entry, 0, len(extra)+1)

	bin, err := archive.FromFile(binName, binPath)
	if err != nil {
		return nil, fmt.Errorf("repro: adding binary: %w", err)
	}
	// The compiler's output mode can vary with umask; a release binary is
	// always executable.
	bin.Executable = true
	entries = append(entries, bin)

	for _, name := range extra {
		e, err := archive.FromFile(name, filepath.Join(moduleDir, name))
		if err != nil {
			return nil, fmt.Errorf("repro: adding %s: %w", name, err)
		}
		e.Executable = false
		entries = append(entries, e)
	}
	return entries, nil
}

func writeArchive(path string, format archive.Format, entries []archive.Entry, modTime time.Time) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("repro: creating archive: %w", err)
	}
	if err := archive.Write(f, format, entries, modTime); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("repro: hashing: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("repro: hashing %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
