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
	"github.com/danielriddell21/letsgo/internal/randomart"
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
	r := newRelease(t)

	bin := filepath.Join(t.TempDir(), "bin")
	out := r.run(t, "-b", bin)

	installed := filepath.Join(bin, "foo")
	info, err := os.Stat(installed)
	if err != nil {
		t.Fatalf("nothing was installed: %v\n%s", err, out)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("%s is not executable", installed)
	}
	if !strings.Contains(out, r.art) {
		t.Errorf("a verified install did not end with the fingerprint:\n%s", out)
	}

	// A substituted archive must be refused, which is the entire reason the
	// digests are baked into the script rather than downloaded beside it.
	writeArchive(t, filepath.Join(r.serve, r.archive), "foo", "#!/bin/sh\necho tampered\n")

	out, err = r.try(t, "-b", t.TempDir())
	if err == nil {
		t.Fatalf("a tampered archive was installed:\n%s", out)
	}
	if !strings.Contains(out, "does not match its recorded digest") {
		t.Errorf("unhelpful failure:\n%s", out)
	}
	// ADR-0015: a fingerprint is never shown for something that failed.
	if strings.Contains(out, "SHA256]") {
		t.Errorf("a failed install showed a fingerprint:\n%s", out)
	}
}

// The manifest is what the fingerprint is drawn from, so it is checked like
// an archive: a substituted one stops the install before anything else.
func TestScriptRefusesASubstitutedManifest(t *testing.T) {
	r := newRelease(t)
	if err := os.WriteFile(filepath.Join(r.serve, "letsgo.json"), []byte(`{"version":"6.6.6"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(t.TempDir(), "bin")
	out, err := r.try(t, "-b", bin)
	if err == nil {
		t.Fatalf("a substituted manifest was accepted:\n%s", out)
	}
	if !strings.Contains(out, "letsgo.json does not match its recorded digest") {
		t.Errorf("unhelpful failure:\n%s", out)
	}
	if strings.Contains(out, "downloading") {
		t.Errorf("an archive was fetched after the manifest failed:\n%s", out)
	}
	if _, err := os.Stat(bin); err == nil {
		t.Error("the install directory was created")
	}
}

// -n shows what would be installed, and the fingerprint once the manifest
// has been checked, without downloading an archive or writing anything.
func TestScriptDryRunInstallsNothing(t *testing.T) {
	r := newRelease(t)

	bin := filepath.Join(t.TempDir(), "bin")
	out := r.run(t, "-n", "-b", bin)

	for _, want := range []string{r.archive, "sha256 " + r.sum, r.art, "nothing was installed"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run output is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "downloading") {
		t.Errorf("a dry run downloaded an archive:\n%s", out)
	}
	if _, err := os.Stat(bin); err == nil {
		t.Error("a dry run created the install directory")
	}
}

// The record the script writes is the one ReadRecord reads back, and it
// describes the binary as installed.
func TestScriptWritesAnInstallRecord(t *testing.T) {
	r := newRelease(t)

	bin := filepath.Join(t.TempDir(), "bin")
	r.run(t, "-b", bin)

	installed := filepath.Join(bin, "foo")
	got, err := install.ReadRecord(installed)
	if err != nil || got == nil {
		t.Fatalf("ReadRecord = (%v, %v)", got, err)
	}
	body, err := os.ReadFile(installed)
	if err != nil {
		t.Fatal(err)
	}
	binarySum := sha256.Sum256(body)

	want := install.Record{
		Schema: install.RecordSchema, Via: "install.sh",
		Project: "foo", Version: "1.0.0", Tag: "v1.0.0",
		ManifestSHA256: r.manifestSum,
		Archive:        r.archive, ArchiveSHA256: r.sum,
		Binary: "foo", BinarySHA256: hex.EncodeToString(binarySum[:]),
	}
	if *got != want {
		t.Errorf("record = %+v\nwant     %+v", *got, want)
	}
}

func TestScriptFingerprintNeedsAManifestDigest(t *testing.T) {
	_, err := install.Script(install.Options{
		Project: "foo", Version: "1.0.0", BaseURL: "https://example.com",
		Targets:     []install.Target{{OS: "linux", Arch: "amd64", Archive: "foo.tar.gz"}},
		Fingerprint: true,
	})
	if err == nil {
		t.Fatal("want an error for a fingerprint with no manifest digest")
	}
}

// release is a published release on disk and the installer generated for it.
type release struct {
	serve, archive, sum, manifestSum, art, script string
}

func newRelease(t *testing.T) *release {
	t.Helper()
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

	r := &release{serve: t.TempDir()}
	r.archive = "foo_1.0.0_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	r.sum = writeArchive(t, filepath.Join(r.serve, r.archive), "foo", "#!/bin/sh\necho hello\n")

	manifest := []byte(`{"project":"foo","version":"1.0.0"}`)
	if err := os.WriteFile(filepath.Join(r.serve, "letsgo.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(manifest)
	r.manifestSum = hex.EncodeToString(digest[:])
	r.art = randomart.Render(digest[:], randomart.Title("v1.0.0"))

	script, err := install.Script(install.Options{
		Project: "foo", Version: "1.0.0", BaseURL: "file://" + r.serve,
		Targets: []install.Target{
			{OS: runtime.GOOS, Arch: runtime.GOARCH, Archive: r.archive, SHA256: r.sum},
		},
		Manifest:    install.File{Name: "letsgo.json", SHA256: r.manifestSum},
		Fingerprint: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.script = filepath.Join(t.TempDir(), install.FileName)
	if err := os.WriteFile(r.script, script, 0o755); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *release) try(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, err := exec.Command("sh", append([]string{r.script}, args...)...).CombinedOutput()
	return string(out), err
}

func (r *release) run(t *testing.T, args ...string) string {
	t.Helper()
	out, err := r.try(t, args...)
	if err != nil {
		t.Fatalf("install.sh %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
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
