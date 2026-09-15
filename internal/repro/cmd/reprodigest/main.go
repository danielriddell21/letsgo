// Command reprodigest builds the reproducibility fixture and prints the
// resulting digests as JSON.
//
// It exists so that CI can compare artifacts produced on different operating
// systems. The in-process tests prove a machine agrees with itself; this
// proves two machines agree with each other, which is the claim a third party
// relies on when they run letsgo verify on hardware we do not control.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/repro"
	"github.com/danielriddell21/letsgo/internal/zig"
)

// cgoTargets are the targets zig cross-compiles cgo to from every host letsgo
// runs on, so that three machines are comparing the same artifacts.
var cgoTargets = []gobuild.Target{
	{OS: "linux", Arch: "amd64"},
	{OS: "linux", Arch: "arm64"},
	{OS: "windows", Arch: "amd64"},
}

// ccObjects compiles one C file per target with the pinned toolchain alone,
// and returns each object's digest.
//
// This is a diagnostic rather than a claim: it isolates zig's compiler from
// everything Go wraps around it, which is the only way to tell which half of a
// cgo build is behaving differently on different machines.
func ccObjects(
	ctx context.Context,
	toolchain zig.Toolchain,
	fixture, workDir string,
) (map[string]string, error) {
	dir := filepath.Join(workDir, "objects")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("reprodigest: %w", err)
	}

	out := map[string]string{}
	for _, target := range cgoTargets {
		cc, ok := toolchain.CC(target)
		if !ok {
			continue
		}

		// CC is a command line: the executable, then the target flags.
		fields := strings.Fields(cc)
		object := filepath.Join(dir, target.OS+"-"+target.Arch+".o")

		args := append(append([]string{}, fields[1:]...),
			"-O2", "-c", filepath.Join(fixture, "checksum.c"), "-o", object)

		cmd := exec.CommandContext(ctx, fields[0], args...)
		if printed, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("reprodigest: compiling for %s: %w\n%s", target, err, printed)
		}

		sum, err := digestOf(object)
		if err != nil {
			return nil, err
		}
		out[target.String()] = sum
	}
	return out, nil
}

func digestOf(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // a path this command built
	if err != nil {
		return "", fmt.Errorf("reprodigest: %w", err)
	}
	defer func() { _ = f.Close() }()

	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", fmt.Errorf("reprodigest: %w", err)
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "reprodigest:", err)
		os.Exit(1)
	}
}

func run() error {
	fixture := flag.String("fixture", "internal/repro/testdata/fixture", "module to build")
	cgoFixture := flag.String("cgo-fixture", "internal/repro/testdata/cgofixture",
		"cgo module to build; empty skips the cgo digests")
	work := flag.String("work", "", "work directory (default: a temporary directory)")
	flag.Parse()

	workDir := *work
	if workDir == "" {
		dir, err := os.MkdirTemp("", "reprodigest-")
		if err != nil {
			return fmt.Errorf("reprodigest: scratch directory: %w", err)
		}
		defer func() { _ = os.RemoveAll(dir) }()
		workDir = dir
	}

	// Fixed inputs. Every value that feeds the build is stated here rather
	// than read from the environment, so the only thing that varies between
	// two runs of this command is the machine it runs on.
	modTime := time.Date(2024, 3, 15, 12, 30, 45, 0, time.UTC)

	artifacts, err := repro.Build(context.Background(), repro.Options{
		ModuleDir:  *fixture,
		Commands:   []repro.Command{{Package: ".", Binary: "fixture"}},
		Name:       "fixture",
		Version:    "1.2.3",
		Commit:     "9f2ab1c",
		ModTime:    modTime,
		ExtraFiles: []string{"README.md", "LICENSE"},
		WorkDir:    workDir,
	})
	if err != nil {
		return err
	}

	image, err := repro.ImageDigest(artifacts, "fixture", modTime)
	if err != nil {
		return err
	}

	// The toolchain is a build input, so it belongs in the record. Without it
	// a version mismatch between machines shows up only as digests that
	// differ for no stated reason, which is a slow thing to diagnose.
	version, err := gobuild.Version(context.Background(), "")
	if err != nil {
		return err
	}

	document, err := repro.SBOMDigest(repro.Options{
		Name: "fixture", Version: "1.2.3", Commit: "9f2ab1c", ModTime: modTime,
	}, version, artifacts)
	if err != nil {
		return err
	}

	// The cgo half of the same claim. A C compiler is a build input exactly as
	// the Go one is, so it is pinned and recorded; this proves the pin is
	// enough, by asking three machines with three different system compilers
	// to produce the same bytes.
	//
	// Only the targets zig can cross-compile to: darwin needs Apple's
	// frameworks, so a cgo build for it cannot be produced from Linux or
	// Windows and would make this a comparison of different things.
	var cgo []repro.Artifact
	var objects map[string]string
	cgoVersion := ""
	if *cgoFixture != "" {
		toolchain, err := zig.Ensure(context.Background(), "", "", filepath.Join(workDir, "toolchains"))
		if err != nil {
			return err
		}
		cgoVersion = "zig " + toolchain.Version

		// Compiled with the pinned toolchain and nothing else, so that a
		// difference between two machines can be attributed rather than
		// guessed at: if these objects agree and the linked binaries do not,
		// the fault is on the Go side; if they disagree, it is zig's own
		// codegen and no amount of flags will fix it here.
		objects, err = ccObjects(context.Background(), toolchain, *cgoFixture, workDir)
		if err != nil {
			return err
		}

		cgo, err = repro.Build(context.Background(), repro.Options{
			ModuleDir:  *cgoFixture,
			Commands:   []repro.Command{{Package: ".", Binary: "cgofixture"}},
			Name:       "cgofixture",
			Version:    "1.2.3",
			Commit:     "9f2ab1c",
			ModTime:    modTime,
			Targets:    cgoTargets,
			ExtraFiles: []string{"README.md", "LICENSE"},
			CGo:        toolchain,
			WorkDir:    filepath.Join(workDir, "cgo"),
		})
		if err != nil {
			return err
		}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")

	if err := enc.Encode(struct {
		// Host is what compiled this, and is the one field that is meant to
		// differ between runners. The comparison uses it to decide what two
		// reports may be held to: everything outside the cgo fields must match
		// across all of them, and the cgo fields only within one host.
		Host string `json:"host"`

		Go           string            `json:"go"`
		CC           string            `json:"cc,omitempty"`
		CCObjects    map[string]string `json:"cc_objects,omitempty"`
		Image        string            `json:"image"`
		SBOM         string            `json:"sbom"`
		Artifacts    []repro.Artifact  `json:"artifacts"`
		CGoArtifacts []repro.Artifact  `json:"cgo_artifacts,omitempty"`
	}{
		Host: gobuild.Host().String(),
		Go:   version, CC: cgoVersion, CCObjects: objects, Image: image, SBOM: document,
		Artifacts: artifacts, CGoArtifacts: cgo,
	}); err != nil {
		return fmt.Errorf("reprodigest: %w", err)
	}
	return nil
}
