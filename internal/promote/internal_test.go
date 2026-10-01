package promote

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/release"
	"github.com/danielriddell21/letsgo/internal/semver"
)

func TestStableTagFor(t *testing.T) {
	tests := []struct {
		name    string
		rcTag   string
		prefix  string
		want    string
		wantErr string
	}{
		{name: "root module rc", rcTag: "v1.3.0-rc.1", prefix: "", want: "v1.3.0"},
		{name: "root module beta", rcTag: "v2.0.0-beta.2", prefix: "", want: "v2.0.0"},
		{name: "scoped module", rcTag: "services/api/v1.5.0-rc.1", prefix: "services/api/", want: "services/api/v1.5.0"},
		{name: "not a prerelease", rcTag: "v1.3.0", prefix: "", wantErr: "is not a prerelease"},
		{name: "outside the scope", rcTag: "v1.3.0-rc.1", prefix: "services/api/", wantErr: "not a version tag in this module's scope"},
		{name: "does not parse", rcTag: "not-a-version", prefix: "", wantErr: "not a version tag in this module's scope"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := stableTagFor(tc.rcTag, tc.prefix)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("stableTagFor(%q, %q): %v", tc.rcTag, tc.prefix, err)
			}
			if got != tc.want {
				t.Errorf("stableTagFor(%q, %q) = %q, want %q", tc.rcTag, tc.prefix, got, tc.want)
			}
		})
	}
}

func TestPrereleasesOf(t *testing.T) {
	scope := discover.Scope{}
	tags := []string{
		"v1.2.0",        // an earlier stable release
		"v1.3.0-rc.1",   // the target's own first RC
		"v1.3.0-rc.2",   // the target's own second RC
		"v1.3.0-beta.1", // a differently-labelled prerelease of the same target
		"v1.4.0-rc.1",   // a prerelease of a different version entirely
		"v1.3.0",        // the stable tag itself, not a prerelease
	}
	target, ok := semver.Parse("1.3.0")
	if !ok {
		t.Fatal("test fixture does not parse")
	}

	got := prereleasesOf(tags, scope, target)

	want := []string{"v1.3.0-rc.2", "v1.3.0-rc.1", "v1.3.0-beta.1"}
	if len(got) != len(want) {
		t.Fatalf("prereleasesOf = %v, want %v", got, want)
	}
	// rc.2 must sort ahead of rc.1 (newest first); beta.1 must be included
	// as a prerelease of the same target even though its label differs.
	if got[0] != "v1.3.0-rc.2" || got[1] != "v1.3.0-rc.1" {
		t.Errorf("prereleasesOf = %v, want the two RCs newest first", got)
	}
	for _, tag := range got {
		if tag == "v1.3.0" || tag == "v1.2.0" || tag == "v1.4.0-rc.1" {
			t.Errorf("prereleasesOf included %s, which is not a prerelease of exactly 1.3.0", tag)
		}
	}
}

func TestPrereleasesOfNoneMatch(t *testing.T) {
	scope := discover.Scope{}
	target, _ := semver.Parse("2.0.0")
	got := prereleasesOf([]string{"v1.2.0", "v1.3.0-rc.1"}, scope, target)
	if len(got) != 0 {
		t.Errorf("prereleasesOf = %v, want none for a target with no prereleases", got)
	}
}

func TestRewriteManifestNamesWhatItCouldNotWrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prepare func(t *testing.T) *release.Result
		wantErr string
	}{
		{
			name: "the release directory is missing",
			prepare: func(t *testing.T) *release.Result {
				t.Helper()
				return &release.Result{Dir: filepath.Join(t.TempDir(), "gone"), Manifest: &manifest.Manifest{}}
			},
			wantErr: "promote: release: writing the manifest",
		},
		{
			name: "a listed file is missing",
			prepare: func(t *testing.T) *release.Result {
				t.Helper()
				return &release.Result{Dir: t.TempDir(), Manifest: &manifest.Manifest{}, Files: []string{"absent.tar.gz"}}
			},
			wantErr: "promote: release: open",
		},
		{
			name: "the checksum file cannot be replaced",
			prepare: func(t *testing.T) *release.Result {
				t.Helper()
				dir := t.TempDir()
				if err := os.Mkdir(filepath.Join(dir, build.ChecksumFile), 0o700); err != nil {
					t.Fatal(err)
				}
				return &release.Result{Dir: dir, Manifest: &manifest.Manifest{}}
			},
			wantErr: "promote: release: writing " + build.ChecksumFile,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := rewriteManifest(tc.prepare(t))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("rewriteManifest error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestManifestDigestNamesAMissingManifest(t *testing.T) {
	t.Parallel()

	_, err := manifestDigest(&release.Result{Dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "promote: hashing the manifest") {
		t.Errorf("manifestDigest error = %v, want it to name the manifest", err)
	}
}

func TestFindReleaseNamesTheListingItCouldNotRead(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	client := github.New("token")
	client.SetEndpoints(server.URL, server.URL)

	_, err := findRelease(context.Background(), Options{Client: client}, "v1.0.0")
	if err == nil || !strings.Contains(err.Error(), "promote: listing releases") {
		t.Errorf("findRelease error = %v, want it to name the listing", err)
	}
}

func TestBuildNotesNamesWhatItCouldNotCollect(t *testing.T) {
	t.Parallel()

	_, err := buildNotes(context.Background(), Options{Dir: t.TempDir(), GitBin: "git"}, "v1.0.0", &plan.Plan{}, &release.Result{})
	if err == nil || !strings.Contains(err.Error(), "promote: collecting the commits") {
		t.Errorf("buildNotes error = %v, want it to name the commits", err)
	}
}

func TestBuildNotesNamesTheManifestItCouldNotDigest(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "feat: first"},
		{"tag", "v1.0.0"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	o := Options{Dir: dir, GitBin: "git", Shallow: true}
	_, err := buildNotes(context.Background(), o, "v1.0.0", &plan.Plan{}, &release.Result{Dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "promote: digesting the manifest") {
		t.Errorf("buildNotes error = %v, want it to name the manifest digest", err)
	}
}
