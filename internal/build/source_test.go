package build_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danielriddell21/letsgo/internal/build"
)

var commitTime = time.Date(2024, 3, 15, 12, 30, 45, 0, time.UTC)

func sourceRepo(t *testing.T) string {
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

	write("go.mod", "module example.com/foo\n\ngo 1.24\n")
	write("main.go", "package main\n\nfunc main() {}\n")
	write("internal/lib/lib.go", "package lib\n")
	write(".gitignore", "dist/\nsecret.txt\n")
	write("dist/artifact.tar.gz", "build output")
	write("secret.txt", "not for publication")

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("add", ".")
	run("commit", "-q", "-m", "first")

	return dir
}

func writeSource(t *testing.T, repo, work string) build.Source {
	t.Helper()
	src, err := build.WriteSource(context.Background(), build.SourceOptions{
		ModuleDir: repo,
		Name:      "foo",
		Version:   "1.2.3",
		ModTime:   commitTime,
		WorkDir:   work,
	})
	if err != nil {
		t.Fatalf("WriteSource: %v", err)
	}
	return src
}

func TestSourceArchiveIsReproducible(t *testing.T) {
	repo := sourceRepo(t)

	first := writeSource(t, repo, t.TempDir())
	time.Sleep(1100 * time.Millisecond) // cross a whole-second boundary
	second := writeSource(t, repo, t.TempDir())

	if first.SHA256 != second.SHA256 {
		t.Errorf("source archive is not reproducible:\n  %s\n  %s", first.SHA256, second.SHA256)
	}
	if first.Name != "foo_1.2.3_source.tar.gz" {
		t.Errorf("Name = %q", first.Name)
	}
	if first.Size == 0 {
		t.Error("Size = 0")
	}
}

func TestSourceArchiveContents(t *testing.T) {
	repo := sourceRepo(t)
	work := t.TempDir()
	src := writeSource(t, repo, work)

	names := map[string]bool{}
	f, err := os.Open(filepath.Join(work, src.Name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names[hdr.Name] = true

		// Extracting must not scatter files across the current directory.
		if !strings.HasPrefix(hdr.Name, "foo-1.2.3/") {
			t.Errorf("%s is not under the version prefix", hdr.Name)
		}
		if hdr.Uid != 0 || hdr.Gid != 0 {
			t.Errorf("%s: uid/gid = %d/%d, want 0/0", hdr.Name, hdr.Uid, hdr.Gid)
		}
		if !hdr.ModTime.Equal(commitTime) {
			t.Errorf("%s: modtime = %s, want the commit time", hdr.Name, hdr.ModTime)
		}
	}

	for _, want := range []string{"go.mod", "main.go", "internal/lib/lib.go", ".gitignore"} {
		if !names["foo-1.2.3/"+want] {
			t.Errorf("%s is missing from the source archive", want)
		}
	}
	// Tracked files are the definition of the source, so build output and
	// anything .gitignore covers is already excluded without a second rule.
	for _, unwanted := range []string{"dist/artifact.tar.gz", "secret.txt"} {
		if names["foo-1.2.3/"+unwanted] {
			t.Errorf("%s should not be in the source archive", unwanted)
		}
	}
}

func TestSourceArchiveRequiresTrackedFiles(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", "-b", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	_, err := build.WriteSource(context.Background(), build.SourceOptions{
		ModuleDir: dir, Name: "foo", Version: "1.0.0", ModTime: commitTime, WorkDir: t.TempDir(),
	})
	if err == nil {
		t.Error("WriteSource succeeded with no tracked files, want an error")
	}
}
