// Package build compiles and packages release artifacts.
//
// This is the real pipeline, not a test harness. The reproducibility suite in
// internal/repro drives this package rather than a parallel implementation,
// which is the only arrangement where a green cross-machine CI run says
// anything about what letsgo actually ships.
//
// Digests are reported separately for the raw binary and for the archive that
// wraps it. When a comparison fails, that distinction immediately says whether
// the compiler or the archive writer is at fault, which is the difference
// between a hard problem and an easy one.
package build

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
	"github.com/danielriddell21/letsgo/internal/zig"
)

// Options describes a set of artifacts to produce.
type Options struct {
	// ModuleDir is the module to build.
	ModuleDir string

	// Commands are the binaries to compile into each archive. One is the
	// ordinary case; several means a module whose product is the collection,
	// shipped as one archive per target rather than one per tool.
	Commands []Command

	// Name is the archive's base name. With a single command it is also the
	// binary's name.
	Name string

	// Version and Commit are injected through the linker.
	Version string
	Commit  string

	// ModTime is applied to every archive entry. In a real release this is the
	// commit timestamp; it must never come from the clock.
	ModTime time.Time

	// Targets to build. Required.
	Targets []gobuild.Target

	// FilesDir is what ExtraFiles are relative to. Empty means ModuleDir.
	//
	// Separate because a nested module still ships the repository's README and
	// licence: the build moves, the documentation does not.
	FilesDir string

	// ExtraFiles are paths relative to FilesDir to include in each archive.
	ExtraFiles []string

	// WorkDir is scratch space for binaries and archives. Required.
	WorkDir string

	// GoBin overrides the toolchain binary.
	GoBin string

	// ExtraLDFlags are appended after the version-injection flags.
	ExtraLDFlags []string

	// ExactLDFlags, when set, replaces the derived version-injection flags
	// entirely. Verification uses it to replay the flags a release recorded
	// rather than reconstruct them and hope the reconstruction matches.
	ExactLDFlags []string

	// Toolchain pins the Go version, e.g. "go1.24.7".
	Toolchain string

	// Tags are build tags passed to the compiler.
	Tags []string

	// CGo is the C toolchain to compile cgo with. Zero means cgo stays off.
	CGo zig.Toolchain

	// Symbols names the variables the version metadata is injected into.
	// Zero means main.version, main.commit and main.date.
	Symbols VersionSymbols

	// CacheKey identifies the source these binaries are built from, enabling
	// reuse across runs. Empty disables caching, which is correct whenever the
	// inputs are not fully described by a key — a dirty worktree, or a
	// verification, where reusing an earlier build would prove nothing.
	CacheKey string

	// Cache holds previously built binaries. Nil disables caching.
	Cache *Cache

	// Smoke, when set, runs the host-platform binary before the rest of the
	// matrix is built. Skipped automatically when the host is not a target.
	Smoke *Smoke

	// Warnf reports something worth knowing that does not stop the build.
	Warnf func(format string, args ...any)
}

// Command is one binary to compile into an archive.
type Command struct {
	// Package is the package to build, e.g. "." or "./cmd/foo".
	Package string

	// Binary is the executable's name inside the archive, without any
	// platform extension.
	Binary string
}

// Artifact is one built and packaged target.
type Artifact struct {
	// Size is the archive's size in bytes.
	Size int64 `json:"size"`

	// BinarySize is the compiled binary's size before archiving. It is the
	// number a size budget is about: compression varies with the compressor,
	// while what a user actually runs is this.
	BinarySize int64 `json:"binary_size"`

	// LDFlags is the linker flag string exactly as passed, so that a rebuild
	// replays a recorded input rather than reconstructing one and hoping.
	LDFlags string `json:"ldflags"`

	// Archive is the archive filename, e.g. "foo_1.0.0_linux_amd64.tar.gz".
	Archive string `json:"archive"`

	// Binaries are the executables in this archive, in the order they were
	// built, each with its own size and digest. Nothing else in here
	// distinguishes one archive's contents from another's.
	Binaries []Binary `json:"binaries"`

	// Paths maps each binary's name to where it sits on disk, for anything
	// that needs the executable rather than the archive — a container layer,
	// say. Never serialised: it is a fact about this machine, not about the
	// release.
	Paths map[string]string `json:"-"`

	// Target is the GOOS/GOARCH pair, e.g. "linux/amd64".
	Target string `json:"target"`

	// OS and Arch are the same pair split, for consumers that need the parts.
	OS   string `json:"os"`
	Arch string `json:"arch"`

	// ArchiveSHA256 is the digest of the archive.
	ArchiveSHA256 string `json:"archive_sha256"`
}

// Binary is one compiled executable inside an archive.
type Binary struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Run compiles and packages every target, returning the artifacts sorted by
// archive name so that two invocations are directly comparable.
func Run(ctx context.Context, o Options) ([]Artifact, error) {
	if err := o.normalise(); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(o.WorkDir, 0o750); err != nil {
		return nil, fmt.Errorf("build: creating work dir: %w", err)
	}

	ldflags := o.linkerFlags()
	ldflagString := strings.Join(append([]string{"-s", "-w"}, ldflags...), " ")

	// Resolved once, and only when it is going to be used: the cache key has
	// to name the compiler that produced the entry.
	goVersion := ""
	if o.CacheKey != "" && o.Cache != nil {
		version, err := gobuild.Version(ctx, o.GoBin)
		if err != nil {
			return nil, err
		}
		goVersion = version
	}

	// The host target is built first so that the smoke check can run before
	// anything else is compiled. Discovering that the binary does not start is
	// worth a few seconds of build time, not the whole matrix.
	targets := hostFirst(o.Targets)

	out := make([]Artifact, 0, len(targets))
	cached := 0

	for _, target := range targets {
		built, reused, err := o.compileAll(ctx, target, ldflags, goVersion)
		if err != nil {
			return nil, err
		}
		cached += reused

		artifact, err := o.pack(target, built, ldflagString)
		if err != nil {
			return nil, err
		}
		out = append(out, artifact)
	}

	if cached > 0 && o.Warnf != nil {
		o.Warnf("%d of %d binaries reused from the build cache", cached, len(targets)*len(o.Commands))
	}

	slices.SortFunc(out, func(a, b Artifact) int { return strings.Compare(a.Archive, b.Archive) })
	return out, nil
}

// normalise fills in the defaults and rejects an unbuildable request.
func (o *Options) normalise() error {
	switch {
	case o.WorkDir == "":
		return fmt.Errorf("build: WorkDir is required")
	case o.ModuleDir == "":
		return fmt.Errorf("build: ModuleDir is required")
	case len(o.Targets) == 0:
		return fmt.Errorf("build: no targets")
	}
	if o.Name == "" {
		o.Name = "app"
	}
	// One unnamed command is the module root, which is what a single-command
	// repository means and what the zero value should do.
	if len(o.Commands) == 0 {
		o.Commands = []Command{{Package: ".", Binary: o.Name}}
	}
	for i, cmd := range o.Commands {
		if cmd.Package == "" {
			o.Commands[i].Package = "."
		}
		if cmd.Binary == "" {
			o.Commands[i].Binary = o.Name
		}
	}
	if o.FilesDir == "" {
		o.FilesDir = o.ModuleDir
	}
	return nil
}

// compilers returns the C and C++ commands for a target, or empty strings when
// cgo is off.
func (o Options) compilers(target gobuild.Target) (cc, cxx string) {
	if o.CGo.Path == "" {
		return "", ""
	}
	cc, _ = o.CGo.CC(target)
	cxx, _ = o.CGo.CXX(target)
	return cc, cxx
}

// VersionSymbols names the variables the version metadata is injected into,
// fully qualified as the linker writes them.
type VersionSymbols struct {
	Version string
	Commit  string
	Date    string
}

// orDefaults fills in the conventional names. A caller that names none gets
// the main package's, which is what every repository that has not said
// otherwise means.
func (v VersionSymbols) orDefaults() VersionSymbols {
	if v.Version == "" {
		v.Version = "main.version"
	}
	if v.Commit == "" {
		v.Commit = "main.commit"
	}
	if v.Date == "" {
		v.Date = "main.date"
	}
	return v
}

// linkerFlags builds the -X flags that carry the version metadata.
//
// The date is derived from ModTime rather than read from the clock. This is
// the single most common way a release pipeline quietly stops being
// reproducible.
func (o Options) linkerFlags() []string {
	symbols := o.Symbols.orDefaults()
	ldflags := []string{
		"-X", symbols.Version + "=" + o.Version,
		"-X", symbols.Commit + "=" + o.Commit,
		"-X", symbols.Date + "=" + o.ModTime.UTC().Format(time.RFC3339),
	}
	if len(o.ExactLDFlags) > 0 {
		ldflags = o.ExactLDFlags
	}
	return append(ldflags, o.ExtraLDFlags...)
}

// compile produces one target's binary, reusing a cached one where the key
// matches, and reports whether it did.
func (o Options) compile(
	ctx context.Context,
	pkg string,
	target gobuild.Target,
	binPath, key string,
	ldflags []string,
) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(binPath), 0o750); err != nil {
		return false, fmt.Errorf("build: %w", err)
	}

	cc, cxx := o.compilers(target)

	reused := o.Cache.Get(key, binPath)
	if !reused {
		if err := gobuild.Build(ctx, gobuild.Request{
			Dir:       o.ModuleDir,
			Package:   pkg,
			Output:    binPath,
			Tags:      o.Tags,
			CC:        cc,
			CXX:       cxx,
			Target:    target,
			LDFlags:   ldflags,
			GoBin:     o.GoBin,
			Toolchain: o.Toolchain,
		}); err != nil {
			return false, err
		}
		o.Cache.Put(key, binPath)
	}

	if o.Smoke != nil && target == gobuild.Host() {
		warning, err := runSmoke(ctx, binPath, *o.Smoke)
		if err != nil {
			return reused, err
		}
		if warning != "" && o.Warnf != nil {
			o.Warnf("%s", warning)
		}
	}
	return reused, nil
}

// pack wraps one target's binary in its archive and digests both.
// builtBinary is one compiled executable on disk, before archiving.
type builtBinary struct {
	Binary
	nameOnDisk string
	path       string
}

// compileAll produces every command's binary for one target, and reports how
// many came from the cache.
func (o Options) compileAll(
	ctx context.Context,
	target gobuild.Target,
	ldflags []string,
	goVersion string,
) ([]builtBinary, int, error) {
	dir := filepath.Join(o.WorkDir, fmt.Sprintf("%s_%s_%s", o.Name, target.OS, target.Arch))

	built := make([]builtBinary, 0, len(o.Commands))
	cached := 0

	for _, cmd := range o.Commands {
		binName := cmd.Binary + target.Ext()
		binPath := filepath.Join(dir, binName)

		// The key covers everything that determines these bytes. A build
		// reused on a partial key would be a build nobody can account for —
		// and the compiler is a build input, so the resolved version has to be
		// in here. o.Toolchain alone is not enough: it is usually empty, and
		// an entry compiled by one Go release would then be handed back to
		// another.
		var key string
		if o.CacheKey != "" {
			// The C toolchain is in here for the same reason the Go one is: it
			// determines the bytes, so an entry compiled by one compiler must
			// never be handed back for a build using another.
			key = CacheKey(o.CacheKey, cmd.Package, target.String(),
				strings.Join(ldflags, " "), strings.Join(o.Tags, ","),
				o.Toolchain, goVersion, o.CGo.Digest, binName)
		}

		reused, err := o.compile(ctx, cmd.Package, target, binPath, key, ldflags)
		if err != nil {
			return nil, 0, err
		}
		if reused {
			cached++
		}

		sum, err := sha256File(binPath)
		if err != nil {
			return nil, 0, err
		}
		info, err := os.Stat(binPath)
		if err != nil {
			return nil, 0, fmt.Errorf("build: %w", err)
		}

		built = append(built, builtBinary{
			Binary:     Binary{Name: cmd.Binary, Size: info.Size(), SHA256: sum},
			nameOnDisk: binName,
			path:       binPath,
		})
	}
	return built, cached, nil
}

// pack writes one target's archive, holding every binary built for it.
func (o Options) pack(target gobuild.Target, built []builtBinary, ldflagString string) (Artifact, error) {
	format := archive.FormatTarGz
	if target.OS == "windows" {
		format = archive.FormatZip
	}

	entries, err := entriesFor(built, o.FilesDir, o.ExtraFiles)
	if err != nil {
		return Artifact{}, err
	}

	archiveName := fmt.Sprintf("%s_%s_%s_%s%s", o.Name, o.Version, target.OS, target.Arch, format.Ext())
	archivePath := filepath.Join(o.WorkDir, archiveName)

	if err := writeArchive(archivePath, format, entries, o.ModTime); err != nil {
		return Artifact{}, err
	}

	archiveSum, err := sha256File(archivePath)
	if err != nil {
		return Artifact{}, err
	}
	info, err := os.Stat(archivePath)
	if err != nil {
		return Artifact{}, fmt.Errorf("build: %w", err)
	}

	binaries := make([]Binary, len(built))
	paths := make(map[string]string, len(built))
	total := int64(0)
	for i, b := range built {
		binaries[i] = b.Binary
		paths[b.Name] = b.path
		total += b.Size
	}

	return Artifact{
		Archive:  archiveName,
		Binaries: binaries,
		Paths:    paths,
		Target:   target.String(),
		OS:       target.OS,
		Arch:     target.Arch,
		Size:     info.Size(),
		// Summed, so that a size budget still describes what a user installs
		// when an archive carries several tools.
		BinarySize:    total,
		LDFlags:       ldflagString,
		ArchiveSHA256: archiveSum,
	}, nil
}

// hostFirst moves the host target to the front, leaving the rest in order.
func hostFirst(targets []gobuild.Target) []gobuild.Target {
	host := gobuild.Host()
	for i, t := range targets {
		if t == host {
			reordered := append([]gobuild.Target{host}, targets[:i]...)
			return append(reordered, targets[i+1:]...)
		}
	}
	return targets
}

func entriesFor(built []builtBinary, moduleDir string, extra []string) ([]archive.Entry, error) {
	entries := make([]archive.Entry, 0, len(extra)+len(built))

	for _, b := range built {
		bin, err := archive.FromFile(b.nameOnDisk, b.path)
		if err != nil {
			return nil, fmt.Errorf("build: adding binary: %w", err)
		}
		// The compiler's output mode can vary with umask; a release binary is
		// always executable.
		bin.Executable = true
		entries = append(entries, bin)
	}

	for _, name := range extra {
		e, err := archive.FromFile(name, filepath.Join(moduleDir, name))
		if err != nil {
			return nil, fmt.Errorf("build: adding %s: %w", name, err)
		}
		e.Executable = false
		entries = append(entries, e)
	}
	return entries, nil
}

func writeArchive(path string, format archive.Format, entries []archive.Entry, modTime time.Time) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("build: creating archive: %w", err)
	}
	if err := archive.Write(f, format, entries, modTime); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("build: closing archive: %w", err)
	}
	return nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("build: hashing: %w", err)
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("build: hashing %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
