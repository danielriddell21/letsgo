package selfupdate_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/forgetest"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/selfupdate"
)

// options point at the forge, carrying the running version a test cares
// about.
func options(f *forgetest.Forge, current string) selfupdate.Options {
	o := f.Options()
	o.Current = current
	return o
}

func TestCheckFindsANewerRelease(t *testing.T) {
	f := forgetest.New(t, "you/tool", "v1.3.0")
	f.Publish(t, "tool", "1.3.0", "tool", "new binary bytes")

	update, err := selfupdate.Check(context.Background(), options(f, "1.2.0"))
	if err != nil {
		t.Fatal(err)
	}
	if update == nil {
		t.Fatal("no update offered")
	}

	if update.Version != "1.3.0" || update.Tag != "v1.3.0" {
		t.Errorf("update = %+v", update)
	}
	if update.Binary != "tool" || !strings.HasSuffix(update.Archive, "linux_amd64.tar.gz") {
		t.Errorf("update names %q in %q", update.Binary, update.Archive)
	}
	// A program should be able to show what changed before replacing itself.
	if !strings.Contains(update.Notes, "something") {
		t.Errorf("notes = %q", update.Notes)
	}
}

// The common case, and deliberately not an error.
func TestCheckReportsNothingWhenCurrent(t *testing.T) {
	f := forgetest.New(t, "you/tool", "v1.3.0")
	f.Publish(t, "tool", "1.3.0", "tool", "bytes")

	for _, current := range []string{"1.3.0", "v1.3.0", "1.4.0"} {
		update, err := selfupdate.Check(context.Background(), options(f, current))
		if err != nil {
			t.Fatalf("Check(%q): %v", current, err)
		}
		if update != nil {
			t.Errorf("Check(%q) offered %s", current, update.Version)
		}
	}
}

// A development build has no claim to being ahead of a release.
func TestCheckTreatsAnUnparseableVersionAsOld(t *testing.T) {
	f := forgetest.New(t, "you/tool", "v1.3.0")
	f.Publish(t, "tool", "1.3.0", "tool", "bytes")

	update, err := selfupdate.Check(context.Background(), options(f, "dev"))
	if err != nil {
		t.Fatal(err)
	}
	if update == nil {
		t.Fatal("a dev build should be offered the release")
	}
}

func TestApplyToReplacesTheBinary(t *testing.T) {
	f := forgetest.New(t, "you/tool", "v1.3.0")
	f.Publish(t, "tool", "1.3.0", "tool", "new binary bytes")

	update, err := selfupdate.Check(context.Background(), options(f, "1.2.0"))
	if err != nil || update == nil {
		t.Fatalf("Check: %v", err)
	}

	target := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(target, []byte("old binary bytes"), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}

	if err := update.ApplyTo(context.Background(), target); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new binary bytes" {
		t.Errorf("installed %q", got)
	}

	// An updater that installs something the user cannot run has not updated
	// anything — everywhere the distinction exists. Windows has no execute
	// bit, so there is nothing to assert there.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Errorf("the installed binary is not executable: %v", info.Mode())
		}
	}
}

// The whole point: a substituted archive must not be installed.
func TestDownloadRefusesATamperedArchive(t *testing.T) {
	f := forgetest.New(t, "you/tool", "v1.3.0")
	f.Publish(t, "tool", "1.3.0", "tool", "new binary bytes")

	update, err := selfupdate.Check(context.Background(), options(f, "1.2.0"))
	if err != nil || update == nil {
		t.Fatalf("Check: %v", err)
	}

	// Swap the archive after the manifest was published, as an attacker with
	// write access to the release page would.
	f.Archives[update.Archive] = forgetest.TarGz(t, "tool", "malicious bytes")

	_, err = update.Download(context.Background())
	if err == nil {
		t.Fatal("a substituted archive was accepted")
	}
	if !strings.Contains(err.Error(), "refusing to install") {
		t.Errorf("unhelpful error: %v", err)
	}
}

// A correct archive containing the wrong binary is a thing that can happen,
// which is why the manifest records both digests.
func TestDownloadRefusesAWrongBinaryInACorrectArchive(t *testing.T) {
	f := forgetest.New(t, "you/tool", "v1.3.0")
	f.Publish(t, "tool", "1.3.0", "tool", "new binary bytes")

	// Re-point the manifest's binary digest at something else, leaving the
	// archive digest correct.
	m := f.DecodeManifest(t)
	m.Artifacts[0].BinarySHA256 = forgetest.Sum([]byte("a different binary"))
	f.SetManifest(t, m)

	update, err := selfupdate.Check(context.Background(), options(f, "1.2.0"))
	if err != nil || update == nil {
		t.Fatalf("Check: %v", err)
	}

	if _, err := update.Download(context.Background()); err == nil {
		t.Fatal("the archive's contents were not checked")
	}
}

func TestCheckReportsAMissingPlatform(t *testing.T) {
	f := forgetest.New(t, "you/tool", "v1.3.0")
	f.Publish(t, "tool", "1.3.0", "tool", "bytes")

	o := options(f, "1.2.0")
	o.OS, o.Arch = "plan9", "riscv64"

	if _, err := selfupdate.Check(context.Background(), o); err == nil {
		t.Fatal("want an error for a platform the release does not build")
	}
}

// Only releases published by letsgo carry a manifest, and without one there is
// nothing describing what to download.
func TestCheckReportsAReleaseWithNoManifest(t *testing.T) {
	f := forgetest.New(t, "you/tool", "v1.3.0")
	f.Manifest = nil
	f.Archives["tool_1.3.0_linux_amd64.tar.gz"] = []byte("x")

	_, err := selfupdate.Check(context.Background(), options(f, "1.2.0"))
	if err == nil || !strings.Contains(err.Error(), manifest.FileName) {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckNeedsARepository(t *testing.T) {
	if _, err := selfupdate.Check(context.Background(), selfupdate.Options{}); err == nil {
		t.Fatal("want an error without a repository")
	}
}

// The Windows path has its own archive format and its own executable suffix,
// and neither is exercised by the tar.gz case.
func TestWindowsReleaseIsAZip(t *testing.T) {
	f := forgetest.New(t, "you/tool", "v1.3.0")
	f.PublishZip(t, "tool", "1.3.0", "tool", "new windows bytes")

	o := options(f, "1.2.0")
	o.OS, o.Arch = "windows", "amd64"

	update, err := selfupdate.Check(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if update == nil {
		t.Fatal("no update offered")
	}
	if update.Binary != "tool.exe" {
		t.Errorf("binary = %q, want tool.exe", update.Binary)
	}

	binary, err := update.Download(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(binary) != "new windows bytes" {
		t.Errorf("downloaded %q", binary)
	}
}

func TestZipWithoutTheBinaryIsReported(t *testing.T) {
	f := forgetest.New(t, "you/tool", "v1.3.0")
	f.PublishZip(t, "tool", "1.3.0", "tool", "bytes")

	// An archive whose digest is right but whose contents are not what the
	// manifest names.
	replacement := forgetest.Zip(t, "something-else.exe", "bytes")
	m := f.DecodeManifest(t)
	m.Artifacts[0].SHA256 = forgetest.Sum(replacement)
	f.SetManifest(t, m)
	f.Archives[m.Artifacts[0].Name] = replacement

	o := options(f, "1.2.0")
	o.OS, o.Arch = "windows", "amd64"

	update, err := selfupdate.Check(context.Background(), o)
	if err != nil || update == nil {
		t.Fatalf("Check: %v", err)
	}

	_, err = update.Download(context.Background())
	if err == nil || !strings.Contains(err.Error(), "contains no tool.exe") {
		t.Fatalf("err = %v", err)
	}
}

func TestTarGzWithoutTheBinaryIsReported(t *testing.T) {
	f := forgetest.New(t, "you/tool", "v1.3.0")
	f.Publish(t, "tool", "1.3.0", "not-the-binary", "bytes")

	update, err := selfupdate.Check(context.Background(), options(f, "1.2.0"))
	if err != nil || update == nil {
		t.Fatalf("Check: %v", err)
	}

	// The manifest names a binary the archive does not hold.
	update.Binary = "tool"
	if _, err := update.Download(context.Background()); err == nil {
		t.Fatal("a missing binary was not reported")
	}
}

// A forge that is down says nothing about the release, and must not look like
// a corrupt one.
func TestCheckReportsAForgeFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	_, err := selfupdate.Check(context.Background(), selfupdate.Options{
		Repo: "you/tool", Current: "1.0.0", APIEndpoint: server.URL, OS: "linux", Arch: "amd64",
	})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckReportsAnEmptyRepository(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)

	_, err := selfupdate.Check(context.Background(), selfupdate.Options{
		Repo: "you/tool", Current: "1.0.0", APIEndpoint: server.URL, OS: "linux", Arch: "amd64",
	})
	if err == nil || !strings.Contains(err.Error(), "no releases") {
		t.Fatalf("err = %v", err)
	}
}

// The archive is described but not attached: a release page that was edited
// after publication.
func TestCheckReportsAMissingArchive(t *testing.T) {
	f := forgetest.New(t, "you/tool", "v1.3.0")
	f.Publish(t, "tool", "1.3.0", "tool", "bytes")
	f.Archives = map[string][]byte{} // described by the manifest, no longer attached

	_, err := selfupdate.Check(context.Background(), options(f, "1.2.0"))
	if err == nil || !strings.Contains(err.Error(), "does not attach") {
		t.Fatalf("err = %v", err)
	}
}

// Writing into a directory that does not exist is the shape of "installed
// somewhere this process cannot write", and has to say so rather than panic.
func TestApplyToReportsAnUnwritableTarget(t *testing.T) {
	f := forgetest.New(t, "you/tool", "v1.3.0")
	f.Publish(t, "tool", "1.3.0", "tool", "bytes")

	update, err := selfupdate.Check(context.Background(), options(f, "1.2.0"))
	if err != nil || update == nil {
		t.Fatalf("Check: %v", err)
	}

	target := filepath.Join(t.TempDir(), "no-such-directory", "tool")
	if err := update.ApplyTo(context.Background(), target); err == nil {
		t.Fatal("want an error for an unwritable target")
	}
}

// A repository shipping three commands publishes three archives for one
// platform, and the caller has to be able to say which one it wants.
func TestCheckPicksTheNamedBinary(t *testing.T) {
	f := forgetest.New(t, "you/tool", "v0.2.0")
	f.PublishCommands(t, "tool", "0.2.0", "letsgo-multi", "letsgo-env", "letsgo-cask")

	options := options(f, "")
	options.Binary = "letsgo-env"

	update, err := selfupdate.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if update == nil {
		t.Fatal("no release offered")
	}
	if update.Binary != "letsgo-env" {
		t.Errorf("Binary = %q, want letsgo-env", update.Binary)
	}
	if !strings.HasPrefix(update.Archive, "letsgo-env_") {
		t.Errorf("Archive = %q, want the letsgo-env archive", update.Archive)
	}

	binary, err := update.Download(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(binary) != "letsgo-env bytes" {
		t.Errorf("downloaded %q", binary)
	}
}

func TestCheckReportsAMissingBinary(t *testing.T) {
	f := forgetest.New(t, "you/tool", "v0.2.0")
	f.PublishCommands(t, "tool", "0.2.0", "letsgo-multi")

	options := options(f, "")
	options.Binary = "letsgo-nope"

	_, err := selfupdate.Check(context.Background(), options)
	if err == nil {
		t.Fatal("a plugin the release does not build should be an error")
	}
	if !strings.Contains(err.Error(), "letsgo-nope") {
		t.Errorf("err = %v", err)
	}
}

// Asking for a tag is asking for that tag, including one older than what is
// running: installing a pinned plugin version is not an update check.
func TestCheckByTagIgnoresTheVersionComparison(t *testing.T) {
	f := forgetest.New(t, "you/tool", "v1.0.0")
	f.Publish(t, "tool", "1.0.0", "tool", "old binary bytes")

	options := options(f, "2.0.0")
	options.Tag = "v1.0.0"

	update, err := selfupdate.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if update == nil {
		t.Fatal("an explicit tag should be offered even when it is older")
	}
	if update.Tag != "v1.0.0" {
		t.Errorf("Tag = %q", update.Tag)
	}
}

func TestCheckReportsAnUnknownTag(t *testing.T) {
	f := forgetest.New(t, "you/tool", "v1.0.0")
	f.Publish(t, "tool", "1.0.0", "tool", "bytes")

	options := options(f, "")
	options.Tag = "v9.9.9"

	if _, err := selfupdate.Check(context.Background(), options); err == nil {
		t.Fatal("an unknown tag should be an error")
	}
}
