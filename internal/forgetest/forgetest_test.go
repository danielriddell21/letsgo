package forgetest_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/forgetest"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/selfupdate"
)

// Every other test in the module trusts this forge to be a faithful model of a
// letsgo release. A fixture nothing checks is a fixture that can quietly stop
// resembling the thing it stands for, and take a suite of passing tests with
// it.

func TestForgeServesAPublishedRelease(t *testing.T) {
	f := forgetest.New(t, "you/tool", "v1.3.0")
	f.Publish(t, "tool", "1.3.0", "tool", "the bytes")

	options := f.Options()
	options.Current = "1.2.0"

	update, err := selfupdate.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if update == nil {
		t.Fatal("a published release should be offered")
	}
	if update.Tag != "v1.3.0" || update.Version != "1.3.0" {
		t.Errorf("update = %+v", update)
	}
	if !strings.Contains(update.Notes, "something") {
		t.Errorf("notes = %q, want the default body", update.Notes)
	}

	// The archive has to survive both digest checks, which is the whole
	// property the fixture exists to provide.
	binary, err := update.Download(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(binary) != "the bytes" {
		t.Errorf("downloaded %q", binary)
	}
}

// The by-tag route has to answer as well as the latest one, or a test for an
// exact version silently falls back to the newest release.
func TestForgeServesTheTagRoute(t *testing.T) {
	f := forgetest.New(t, "you/tool", "v1.0.0")
	f.Publish(t, "tool", "1.0.0", "tool", "old bytes")

	options := f.Options()
	options.Current = "2.0.0" // newer than the release, so only the tag can find it
	options.Tag = "v1.0.0"

	update, err := selfupdate.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if update == nil || update.Tag != "v1.0.0" {
		t.Fatalf("update = %+v", update)
	}
}

func TestForgeServesSeveralCommands(t *testing.T) {
	f := forgetest.New(t, "you/plugins", "v0.2.0")
	f.PublishCommands(t, "plugins", "0.2.0", "one", "two", "three")

	if len(f.Archives) != 3 {
		t.Fatalf("published %d archives, want 3", len(f.Archives))
	}

	options := f.Options()
	options.Binary = "two"

	update, err := selfupdate.Check(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if update == nil || update.Binary != "two" {
		t.Fatalf("update = %+v", update)
	}

	binary, err := update.Download(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Each binary carries its own name, so a test can tell which it got.
	if string(binary) != forgetest.Content("two") {
		t.Errorf("downloaded %q", binary)
	}
}

// The Windows shape is a zip whose executable carries a suffix the manifest
// does not record.
func TestForgeServesAWindowsZip(t *testing.T) {
	f := forgetest.New(t, "you/tool", "v1.3.0")
	f.PublishZip(t, "tool", "1.3.0", "tool", "windows bytes")

	options := f.Options()
	options.Current = "1.2.0"
	options.OS, options.Arch = "windows", "amd64"

	update, err := selfupdate.Check(context.Background(), options)
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
	if string(binary) != "windows bytes" {
		t.Errorf("downloaded %q", binary)
	}
}

// A nil manifest is a release letsgo did not publish, and the asset must then
// be absent rather than served empty.
func TestForgeWithoutAManifest(t *testing.T) {
	f := forgetest.New(t, "you/tool", "v1.3.0")
	f.Publish(t, "tool", "1.3.0", "tool", "bytes")
	f.Manifest = nil

	options := f.Options()
	options.Current = "1.2.0"

	_, err := selfupdate.Check(context.Background(), options)
	if err == nil || !strings.Contains(err.Error(), manifest.FileName) {
		t.Fatalf("err = %v", err)
	}
}

// An artifact the manifest describes but the forge does not hold is a release
// page edited after publication, and has to read as missing.
func TestForgeReportsAMissingArchive(t *testing.T) {
	f := forgetest.New(t, "you/tool", "v1.3.0")

	resp, err := http.Get(f.URL() + "/download/not-published.tar.gz") //nolint:noctx // a fixture check, not a client
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// Tests corrupt one field of a manifest and serve it back; the round trip has
// to preserve everything else.
func TestDecodeManifestRoundTrips(t *testing.T) {
	f := forgetest.New(t, "you/tool", "v1.3.0")
	f.Publish(t, "tool", "1.3.0", "tool", "bytes")

	m := f.DecodeManifest(t)
	if m.Project != "tool" || m.Version != "1.3.0" || len(m.Artifacts) != 1 {
		t.Fatalf("manifest = %+v", m)
	}

	m.Artifacts[0].BinarySHA256 = forgetest.Sum([]byte("something else"))
	f.SetManifest(t, m)

	again := f.DecodeManifest(t)
	if again.Artifacts[0].BinarySHA256 != forgetest.Sum([]byte("something else")) {
		t.Errorf("the edit did not survive: %+v", again.Artifacts[0])
	}
	if again.Artifacts[0].Name != m.Artifacts[0].Name {
		t.Errorf("the round trip lost the artifact name")
	}
}

// The archives are built by letsgo's own writer, so the two formats have to
// come back out as the formats they claim to be.
func TestArchivesAreTheFormatTheyClaim(t *testing.T) {
	tarball := forgetest.TarGz(t, "tool", "bytes")
	if len(tarball) < 2 || tarball[0] != 0x1f || tarball[1] != 0x8b {
		t.Error("TarGz did not produce a gzip stream")
	}

	zipped := forgetest.Zip(t, "tool.exe", "bytes")
	if len(zipped) < 2 || string(zipped[:2]) != "PK" {
		t.Error("Zip did not produce a zip archive")
	}
}

func TestSumIsAHexDigest(t *testing.T) {
	got := forgetest.Sum([]byte("bytes"))
	if len(got) != 64 || strings.ContainsAny(got, "ghijklmnopqrstuvwxyz") {
		t.Errorf("Sum = %q, want 64 hex characters", got)
	}
}

func TestOptionsPointAtTheForge(t *testing.T) {
	f := forgetest.New(t, "you/tool", "v1.3.0")

	options := f.Options()
	if options.APIEndpoint != f.URL() {
		t.Errorf("APIEndpoint = %q, want %q", options.APIEndpoint, f.URL())
	}
	if options.Repo != "you/tool" {
		t.Errorf("Repo = %q", options.Repo)
	}
	if options.OS != "linux" || options.Arch != "amd64" {
		t.Errorf("platform = %s/%s, want linux/amd64", options.OS, options.Arch)
	}
}
