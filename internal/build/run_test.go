package build_test

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/gobuild"
)

// multiRepo is a module whose product is a collection of tools rather than one
// program, which is the case an archive has to carry several binaries for.
func multiRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	write := func(name, content string) {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/tools\n\ngo 1.24\n")
	write("cmd/crabs/main.go", "package main\n\nfunc main() {}\n")
	write("cmd/duck/main.go", "package main\n\nfunc main() {}\n")
	write("README.md", "# tools\n")
	return dir
}

// buildTools compiles both commands into one archive per target, and returns
// the artifacts alongside the directory they were written to.
func buildTools(t *testing.T, targets ...gobuild.Target) ([]build.Artifact, string) {
	t.Helper()

	work := t.TempDir()
	artifacts, err := build.Run(t.Context(), build.Options{
		ModuleDir: multiRepo(t),
		Commands: []build.Command{
			{Package: "./cmd/crabs", Binary: "crabs"},
			{Package: "./cmd/duck", Binary: "duck"},
		},
		Name:       "tools",
		Version:    "1.2.3",
		Commit:     "9f2ab1c",
		ModTime:    commitTime,
		Targets:    targets,
		ExtraFiles: []string{"README.md"},
		WorkDir:    work,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return artifacts, work
}

// One archive per target carrying every command, rather than one archive per
// command: a module of eleven tools would otherwise ship eleven archives per
// platform, and a formula per tool.
func TestRunPacksEveryCommandIntoOneArchive(t *testing.T) {
	artifacts, work := buildTools(t,
		gobuild.Target{OS: "linux", Arch: "amd64"},
		gobuild.Target{OS: "windows", Arch: "amd64"})

	if len(artifacts) != 2 {
		t.Fatalf("built %d artifacts, want one per target", len(artifacts))
	}

	for _, a := range artifacts {
		if len(a.Binaries) != 2 {
			t.Errorf("%s holds %d binaries, want 2", a.Archive, len(a.Binaries))
			continue
		}

		// Sizes and digests are per binary, because no single number
		// describes an archive holding several.
		var names []string
		summed := int64(0)
		for _, b := range a.Binaries {
			names = append(names, b.Name)
			summed += b.Size
			if b.Size == 0 || b.SHA256 == "" {
				t.Errorf("%s in %s has no size or digest", b.Name, a.Archive)
			}
			// Paths are how anything needing the executable rather than the
			// archive — a container layer — finds it.
			if path := a.Paths[b.Name]; path == "" {
				t.Errorf("%s in %s has no path on disk", b.Name, a.Archive)
			} else if _, err := os.Stat(path); err != nil {
				t.Errorf("%s: %v", b.Name, err)
			}
		}
		sort.Strings(names)
		if strings.Join(names, ",") != "crabs,duck" {
			t.Errorf("%s holds %q", a.Archive, names)
		}

		// A size budget is written against what a user installs, which with
		// several tools is all of them.
		if a.BinarySize != summed {
			t.Errorf("%s reports %d bytes of binary, but its binaries sum to %d",
				a.Archive, a.BinarySize, summed)
		}
		if _, err := os.Stat(filepath.Join(work, a.Archive)); err != nil {
			t.Errorf("%s was reported but not written: %v", a.Archive, err)
		}
	}
}

// What a user unpacks. An executable missing from the archive, or a
// documentation file missing from it, is what they see rather than what the
// artifact record claims.
func TestRunArchivesEveryBinaryAndTheExtraFiles(t *testing.T) {
	linux, work := buildTools(t, gobuild.Target{OS: "linux", Arch: "amd64"})

	got := tarEntries(t, filepath.Join(work, linux[0].Archive))
	sort.Strings(got)
	if strings.Join(got, ",") != "README.md,crabs,duck" {
		t.Errorf("tar.gz holds %q", got)
	}

	// Windows gets a zip and an .exe suffix, and must still carry both.
	windows, work := buildTools(t, gobuild.Target{OS: "windows", Arch: "amd64"})
	got = zipEntries(t, filepath.Join(work, windows[0].Archive))
	sort.Strings(got)
	if strings.Join(got, ",") != "README.md,crabs.exe,duck.exe" {
		t.Errorf("zip holds %q", got)
	}
}

func tarEntries(t *testing.T, path string) []string {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening the archive: %v", err)
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}

	var names []string
	r := tar.NewReader(gz)
	for {
		h, err := r.Next()
		if err != nil {
			return names
		}
		names = append(names, h.Name)
	}
}

func zipEntries(t *testing.T, path string) []string {
	t.Helper()

	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("opening the archive: %v", err)
	}
	defer func() { _ = r.Close() }()

	names := make([]string, 0, len(r.File))
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	return names
}

// Two builds of the same package for the same target, differing only in build
// tags, select different files and are not interchangeable. The cache key has
// to say so.
//
// A variant compiles exactly that way, and its archive usually holds a binary
// with the same name as the release's own — so a key blind to tags hands the
// second build the first one's binary and ships it under the variant's name.
func TestRunCachesTagSetsSeparately(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/prog\n\ngo 1.24\n")
	write("main.go", "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(mode) }\n")
	write("mode_plain.go", "//go:build !gui\n\npackage main\n\nconst mode = \"plain\"\n")
	write("mode_gui.go", "//go:build gui\n\npackage main\n\nconst mode = \"gui\"\n")

	cache := build.OpenCache(t.TempDir())
	target := gobuild.Host()

	// One cache key for both, as a single release gives every build of one
	// commit.
	run := func(name string, tags []string) build.Artifact {
		t.Helper()
		artifacts, err := build.Run(t.Context(), build.Options{
			ModuleDir: dir,
			Commands:  []build.Command{{Package: ".", Binary: "prog"}},
			Name:      name,
			Version:   "1.2.3",
			ModTime:   commitTime,
			Targets:   []gobuild.Target{target},
			Tags:      tags,
			WorkDir:   t.TempDir(),
			CacheKey:  "one-commit",
			Cache:     cache,
		})
		if err != nil {
			t.Fatalf("Run(%v): %v", tags, err)
		}
		if len(artifacts) != 1 || len(artifacts[0].Binaries) != 1 {
			t.Fatalf("Run(%v) produced %+v", tags, artifacts)
		}
		return artifacts[0]
	}

	plain := run("prog", nil)
	tagged := run("prog-gui", []string{"gui"})

	if plain.Binaries[0].SHA256 == tagged.Binaries[0].SHA256 {
		t.Error("the tagged build was handed the untagged build's binary from the cache")
	}
}
