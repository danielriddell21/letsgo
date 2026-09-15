package zig

import (
	"archive/tar"
	"archive/zip"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// tarball writes a zig-shaped archive: one root directory holding a zig
// executable. xz because that is what zig publishes, which is the whole reason
// extraction shells out.
func tarball(t *testing.T, dir, name string) string {
	t.Helper()
	if _, err := exec.LookPath("xz"); err != nil {
		t.Skip("xz is not installed")
	}

	plain := filepath.Join(dir, name+".tar")
	f, err := os.Create(plain) //nolint:gosec // a path this test built
	if err != nil {
		t.Fatal(err)
	}

	w := tar.NewWriter(f)
	for _, entry := range []struct {
		name string
		body string
		mode int64
	}{
		{name + "/", "", 0o755},
		{name + "/zig", "#!/bin/sh\necho 0.16.0\n", 0o755},
		{name + "/lib/std.zig", "pub const x = 1;", 0o644},
	} {
		header := &tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.body))}
		if strings.HasSuffix(entry.name, "/") {
			header.Typeflag = tar.TypeDir
			header.Size = 0
		}
		if err := w.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag != tar.TypeDir {
			if _, err := w.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if out, err := exec.Command("xz", "-q", plain).CombinedOutput(); err != nil {
		t.Fatalf("xz: %v\n%s", err, out)
	}
	return plain + ".xz"
}

func TestExtractUnpacksATarball(t *testing.T) {
	staging := t.TempDir()
	archive := tarball(t, staging, "zig-x86_64-linux-0.16.0")

	dest := filepath.Join(t.TempDir(), "0.16.0-abc")
	if err := extract(context.Background(), archive, dest); err != nil {
		t.Fatal(err)
	}

	// The single root directory the archive wraps everything in is flattened
	// away, so the executable is where Ensure says it is.
	if _, err := os.Stat(filepath.Join(dest, "zig")); err != nil {
		t.Errorf("the executable is not at the top: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "lib", "std.zig")); err != nil {
		t.Errorf("the library is missing: %v", err)
	}
}

func TestExtractUnpacksAZip(t *testing.T) {
	staging := t.TempDir()
	archive := filepath.Join(staging, "zig-x86_64-windows-0.16.0.zip")

	f, err := os.Create(archive) //nolint:gosec // a path this test built
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, body := range map[string]string{
		"zig-x86_64-windows-0.16.0/zig.exe":     "MZ",
		"zig-x86_64-windows-0.16.0/lib/std.zig": "pub const x = 1;",
	} {
		entry, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "0.16.0-abc")
	if err := extract(context.Background(), archive, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "zig.exe")); err != nil {
		t.Errorf("the executable is not at the top: %v", err)
	}
}

// An archive that does not hold exactly one root directory is not the shape
// zig publishes, and flattening it would put files somewhere arbitrary.
func TestExtractRefusesAnUnexpectedShape(t *testing.T) {
	staging := t.TempDir()
	archive := filepath.Join(staging, "flat.zip")

	f, err := os.Create(archive) //nolint:gosec // a path this test built
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	entry, err := w.Create("zig")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	err = extract(context.Background(), archive, filepath.Join(t.TempDir(), "dest"))
	if err == nil || !strings.Contains(err.Error(), "single root directory") {
		t.Errorf("err = %v", err)
	}
}

// An entry naming a path outside the destination would write wherever it
// liked. The archive is pinned, so this cannot happen — which is why it is
// cheap to refuse rather than reason about.
func TestUnzipRefusesAnEscapingEntry(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "evil.zip")

	f, err := os.Create(archive) //nolint:gosec // a path this test built
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	entry, err := w.Create("../escaped")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	err = unzip(archive, filepath.Join(dir, "dest"))
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Errorf("err = %v", err)
	}
}

// The paths handed to tar are ones this package built, and are checked rather
// than assumed: exec runs tar with no shell, so what is left to get wrong is a
// relative or unclean path meaning something other than it appears to.
func TestUntarRefusesAPathItDidNotBuild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("path shapes differ on Windows")
	}

	for _, c := range []struct{ archive, dir string }{
		{"relative.tar.xz", "/tmp"},
		{"/tmp/a.tar.xz", "relative"},
		{"/tmp/../tmp/a.tar.xz", "/tmp"},
	} {
		err := untar(context.Background(), c.archive, c.dir)
		if err == nil || !strings.Contains(err.Error(), "path") {
			t.Errorf("untar(%q, %q) = %v", c.archive, c.dir, err)
		}
	}
}

func TestArchiveNameFollowsTheHost(t *testing.T) {
	for host, want := range map[string]string{
		"x86_64-linux":   "zig-x86_64-linux-0.16.0.tar.xz",
		"aarch64-macos":  "zig-aarch64-macos-0.16.0.tar.xz",
		"x86_64-windows": "zig-x86_64-windows-0.16.0.zip",
	} {
		if got := archiveName("0.16.0", host); got != want {
			t.Errorf("archiveName(%q) = %q, want %q", host, got, want)
		}
	}
}

// The host name decides which published archive is fetched, and the suffix
// decides what the extracted executable is called. Both are derived from the
// running platform, so neither can be checked against a fixed answer — only
// against the shape zig publishes under, and against each other.
func TestHostNameMatchesWhatZigPublishes(t *testing.T) {
	host := hostName()
	if host == "" {
		t.Skipf("zig publishes no build for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	arch, goos, ok := strings.Cut(host, "-")
	if !ok {
		t.Fatalf("hostName() = %q, want <arch>-<os>", host)
	}
	switch runtime.GOARCH {
	case "amd64":
		if arch != "x86_64" {
			t.Errorf("amd64 became %q", arch)
		}
	case "arm64":
		if arch != "aarch64" {
			t.Errorf("arm64 became %q", arch)
		}
	}
	// darwin is the one that is not simply the Go name.
	if runtime.GOOS == "darwin" && goos != "macos" {
		t.Errorf("darwin became %q", goos)
	}

	// The archive's format follows from the host, and the two must agree
	// about which platform this is.
	name := archiveName("0.16.0", host)
	if runtime.GOOS == "windows" {
		if !strings.HasSuffix(name, ".zip") || exeSuffix() != ".exe" {
			t.Errorf("windows: archive %q, suffix %q", name, exeSuffix())
		}
		return
	}
	if !strings.HasSuffix(name, ".tar.xz") || exeSuffix() != "" {
		t.Errorf("%s: archive %q, suffix %q", runtime.GOOS, name, exeSuffix())
	}
}
