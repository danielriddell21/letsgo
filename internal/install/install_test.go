package install_test

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/install"
)

func TestScriptNeedsARelease(t *testing.T) {
	if _, err := install.Script(install.Options{Project: "foo"}); err == nil {
		t.Fatal("want an error for an incomplete release")
	}
	// A Windows-only release has nothing a shell script could install, and
	// generating one that always refuses would be worse than generating none.
	_, err := install.Script(install.Options{
		Project: "foo", Version: "1.0.0", BaseURL: "https://example.com",
		Targets: []install.Target{{OS: "windows", Arch: "amd64", Archive: "foo.zip"}},
	})
	if err == nil {
		t.Fatal("want an error for a release with no POSIX targets")
	}
}

func TestScriptIsDeterministic(t *testing.T) {
	opts := install.Options{
		Project: "foo", Version: "1.0.0", BaseURL: "https://example.com/d/",
		Targets: []install.Target{
			{OS: "linux", Arch: "arm64", Archive: "b.tar.gz", SHA256: "bb"},
			{OS: "darwin", Arch: "amd64", Archive: "a.tar.gz", SHA256: "aa"},
		},
	}
	first, err := install.Script(opts)
	if err != nil {
		t.Fatal(err)
	}

	opts.Targets[0], opts.Targets[1] = opts.Targets[1], opts.Targets[0]
	second, err := install.Script(opts)
	if err != nil {
		t.Fatal(err)
	}

	if string(first) != string(second) {
		t.Error("the same release rendered two different scripts")
	}
	// A trailing slash on the base would produce "…/d//a.tar.gz".
	if strings.Contains(string(first), "/d//") {
		t.Error("base URL was not normalised")
	}
}

func TestPlatformsExcludesWindows(t *testing.T) {
	got := install.Platforms([]install.Target{
		{OS: "linux", Arch: "amd64"},
		{OS: "linux", Arch: "amd64"},
		{OS: "windows", Arch: "amd64"},
		{OS: "darwin", Arch: "arm64"},
	})
	want := []string{"darwin/arm64", "linux/amd64"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Platforms = %v, want %v", got, want)
	}
}

// The script is the only part of a release that runs on a stranger's machine,
// so it is exercised rather than merely rendered.
func TestScriptInstallsAndVerifies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the installer is a POSIX shell script")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no POSIX shell")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		if _, err := exec.LookPath("wget"); err != nil {
			t.Skip("neither curl nor wget")
		}
	}

	serve := t.TempDir()
	archive := "foo_1.0.0_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	sum := writeArchive(t, filepath.Join(serve, archive), "foo", "#!/bin/sh\necho hello\n")

	script, err := install.Script(install.Options{
		Project: "foo", Version: "1.0.0", BaseURL: "file://" + serve,
		Targets: []install.Target{
			{OS: runtime.GOOS, Arch: runtime.GOARCH, Archive: archive, SHA256: sum},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	scriptPath := filepath.Join(t.TempDir(), install.FileName)
	if err := os.WriteFile(scriptPath, script, 0o755); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(t.TempDir(), "bin")
	out, err := exec.Command("sh", scriptPath, "-b", bin).CombinedOutput()
	if err != nil {
		t.Fatalf("install.sh: %v\n%s", err, out)
	}

	installed := filepath.Join(bin, "foo")
	info, err := os.Stat(installed)
	if err != nil {
		t.Fatalf("nothing was installed: %v\n%s", err, out)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("%s is not executable", installed)
	}

	// A substituted archive must be refused, which is the entire reason the
	// digests are baked into the script rather than downloaded beside it.
	writeArchive(t, filepath.Join(serve, archive), "foo", "#!/bin/sh\necho tampered\n")

	out, err = exec.Command("sh", scriptPath, "-b", t.TempDir()).CombinedOutput()
	if err == nil {
		t.Fatalf("a tampered archive was installed:\n%s", out)
	}
	if !strings.Contains(string(out), "does not match its recorded digest") {
		t.Errorf("unhelpful failure:\n%s", out)
	}
}

// writeArchive builds a one-binary tarball and returns its SHA-256.
func writeArchive(t *testing.T, path, name, content string) string {
	t.Helper()

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	h := sha256.New()
	gz := gzip.NewWriter(io.MultiWriter(f, h))
	tw := tar.NewWriter(gz)

	body := []byte(content)
	if err := tw.WriteHeader(&tar.Header{
		Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}
