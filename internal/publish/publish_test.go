package publish_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/danielriddell21/letsgo/internal/publish"
	"github.com/danielriddell21/letsgo/internal/publish/github"
)

// fakeGitHub is enough of the releases API to exercise resume behaviour.
type fakeGitHub struct {
	mu sync.Mutex

	release   *github.Release
	assets    map[string]github.Asset
	nextID    int64
	reportSHA bool // whether the forge reports asset digests

	created       int
	updated       int
	uploads       []string
	deletes       []string
	failNext      string // fail the upload of this asset once
	refuseEdits   bool   // reject PATCH with 403, as a restricted token would
	failEditsWith int    // reject PATCH with this status instead
}

func newFake(reportSHA bool) *fakeGitHub {
	return &fakeGitHub{assets: map[string]github.Asset{}, nextID: 100, reportSHA: reportSHA}
}

func (f *fakeGitHub) addAsset(name string, body []byte) {
	sum := sha256.Sum256(body)
	f.nextID++
	asset := github.Asset{ID: f.nextID, Name: name, Size: int64(len(body))}
	if f.reportSHA {
		asset.Digest = "sha256:" + hex.EncodeToString(sum[:])
	}
	f.assets[name] = asset
}

func (f *fakeGitHub) assetList() []github.Asset {
	out := make([]github.Asset, 0, len(f.assets))
	for _, a := range f.assets {
		out = append(out, a)
	}
	return out
}

func (f *fakeGitHub) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()

		write := func(v any) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(v)
		}
		path := r.URL.Path

		switch {
		case r.Method == http.MethodGet && strings.Contains(path, "/releases/tags/"):
			if f.release == nil {
				w.WriteHeader(http.StatusNotFound)
				write(map[string]string{"message": "Not Found"})
				return
			}
			write(f.release)

		case r.Method == http.MethodPost && strings.HasSuffix(path, "/releases"):
			var in github.ReleaseInput
			_ = json.NewDecoder(r.Body).Decode(&in)
			f.created++
			f.release = &github.Release{ID: 42, TagName: in.TagName, Name: in.Name, Body: in.Body}
			write(f.release)

		case r.Method == http.MethodPatch && strings.Contains(path, "/releases/"):
			if f.refuseEdits {
				w.WriteHeader(http.StatusForbidden)
				write(map[string]string{"message": "editing releases is not permitted"})
				return
			}
			if f.failEditsWith != 0 {
				w.WriteHeader(f.failEditsWith)
				write(map[string]string{"message": "server error"})
				return
			}
			var in github.ReleaseInput
			_ = json.NewDecoder(r.Body).Decode(&in)
			f.updated++
			f.release.Body = in.Body
			f.release.Name = in.Name
			write(f.release)

		case r.Method == http.MethodGet && strings.HasSuffix(path, "/assets"):
			write(f.assetList())

		case r.Method == http.MethodDelete && strings.Contains(path, "/releases/assets/"):
			id := path[strings.LastIndex(path, "/")+1:]
			for name, a := range f.assets {
				if fmt.Sprint(a.ID) == id {
					f.deletes = append(f.deletes, name)
					delete(f.assets, name)
				}
			}
			w.WriteHeader(http.StatusNoContent)

		case r.Method == http.MethodPost && strings.Contains(path, "/assets"):
			name := r.URL.Query().Get("name")
			if name == f.failNext {
				f.failNext = ""
				w.WriteHeader(http.StatusBadGateway)
				write(map[string]string{"message": "upstream hiccup"})
				return
			}
			body, _ := io.ReadAll(r.Body)
			f.uploads = append(f.uploads, name)
			f.addAsset(name, body)
			write(f.assets[name])

		case r.Method == http.MethodGet && strings.Count(path, "/") == 3:
			write(map[string]any{"permissions": map[string]bool{"push": true}})

		default:
			w.WriteHeader(http.StatusNotFound)
			write(map[string]string{"message": "unexpected " + r.Method + " " + path})
		}
	})
}

type fixture struct {
	fake  *fakeGitHub
	opts  publish.Options
	files map[string][]byte
}

func setup(t *testing.T, reportSHA bool) *fixture {
	t.Helper()

	fake := newFake(reportSHA)
	server := httptest.NewServer(fake.handler())
	t.Cleanup(server.Close)

	client := github.New("token")
	client.SetEndpoints(server.URL, server.URL)

	dir := t.TempDir()
	files := map[string][]byte{
		"foo_1.0.0_linux_amd64.tar.gz":  []byte("linux artifact contents"),
		"foo_1.0.0_darwin_arm64.tar.gz": []byte("darwin artifact contents"),
		"letsgo.json":                   []byte(`{"schema":1}`),
		"SHA256SUMS":                    []byte("sums\n"),
	}

	sums := map[string]string{}
	var names []string
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(body)
		sums[name] = hex.EncodeToString(sum[:])
		names = append(names, name)
	}
	// Deterministic order so assertions can reason about counts.
	sortStrings(names)

	return &fixture{
		fake:  fake,
		files: files,
		opts: publish.Options{
			Client:  client,
			Repo:    github.Repo{Owner: "you", Name: "foo"},
			Dir:     dir,
			Files:   names,
			Sums:    sums,
			Release: github.ReleaseInput{TagName: "v1.0.0", Name: "v1.0.0", Body: "notes"},
		},
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func TestFirstRunCreatesAndUploadsEverything(t *testing.T) {
	f := setup(t, true)

	result, err := publish.Run(context.Background(), f.opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !result.Created || f.fake.created != 1 {
		t.Errorf("Created = %v, server saw %d creates", result.Created, f.fake.created)
	}
	if len(result.Uploaded) != len(f.files) {
		t.Errorf("uploaded %v, want all %d files", result.Uploaded, len(f.files))
	}
	if len(result.Skipped) != 0 {
		t.Errorf("skipped %v on a first run", result.Skipped)
	}
}

// The property the whole design rests on: running twice does not duplicate,
// conflict, or re-upload.
func TestSecondRunUploadsNothing(t *testing.T) {
	f := setup(t, true)

	if _, err := publish.Run(context.Background(), f.opts); err != nil {
		t.Fatal(err)
	}
	uploadsAfterFirst := len(f.fake.uploads)

	result, err := publish.Run(context.Background(), f.opts)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}

	if len(result.Uploaded) != 0 {
		t.Errorf("re-uploaded %v", result.Uploaded)
	}
	if len(result.Skipped) != len(f.files) {
		t.Errorf("skipped %d files, want %d", len(result.Skipped), len(f.files))
	}
	if len(f.fake.uploads) != uploadsAfterFirst {
		t.Errorf("server saw %d uploads, want %d", len(f.fake.uploads), uploadsAfterFirst)
	}
	if f.fake.created != 1 {
		t.Errorf("created the release %d times", f.fake.created)
	}
}

// The case resume exists for: a run that died partway through.
func TestResumeUploadsOnlyWhatIsMissing(t *testing.T) {
	f := setup(t, true)

	f.fake.failNext = f.opts.Files[1]
	if _, err := publish.Run(context.Background(), f.opts); err == nil {
		t.Fatal("Run succeeded despite an upload failure")
	}

	before := len(f.fake.uploads)

	result, err := publish.Run(context.Background(), f.opts)
	if err != nil {
		t.Fatalf("resumed Run: %v", err)
	}
	if len(result.Uploaded)+len(result.Skipped) != len(f.files) {
		t.Errorf("uploaded %v and skipped %v, want %d in total",
			result.Uploaded, result.Skipped, len(f.files))
	}
	if len(result.Skipped) == 0 {
		t.Error("resume re-uploaded files that were already present")
	}
	if uploaded := len(f.fake.uploads) - before; uploaded != len(result.Uploaded) {
		t.Errorf("server saw %d uploads, result claims %d", uploaded, len(result.Uploaded))
	}
}

// A truncated asset looks present and is not usable.
func TestCorruptAssetIsReplaced(t *testing.T) {
	f := setup(t, true)
	name := f.opts.Files[0]

	f.fake.release = &github.Release{ID: 42, TagName: "v1.0.0"}
	f.fake.addAsset(name, []byte("truncated"))

	result, err := publish.Run(context.Background(), f.opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(result.Replaced) != 1 || result.Replaced[0] != name {
		t.Errorf("Replaced = %v, want [%s]", result.Replaced, name)
	}
	if len(f.fake.deletes) != 1 || f.fake.deletes[0] != name {
		t.Errorf("server saw deletes %v", f.fake.deletes)
	}
	if !contains(result.Uploaded, name) {
		t.Errorf("%s was deleted but not re-uploaded", name)
	}
}

// Where the forge reports no digest, size is the only signal available. It
// still catches the truncated upload resume exists for.
func TestFallsBackToSizeWithoutDigests(t *testing.T) {
	f := setup(t, false)
	name := f.opts.Files[0]

	f.fake.release = &github.Release{ID: 42, TagName: "v1.0.0"}
	f.fake.addAsset(name, f.files[name]) // right size
	f.fake.addAsset(f.opts.Files[1], []byte("x"))

	result, err := publish.Run(context.Background(), f.opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !contains(result.Skipped, name) {
		t.Errorf("a correctly sized asset was not skipped: %v", result.Skipped)
	}
	if !contains(result.Replaced, f.opts.Files[1]) {
		t.Errorf("a truncated asset was not replaced: %v", result.Replaced)
	}
}

// The default: a release letsgo publishes describes what letsgo built, and a
// re-run after a corrected changelog carries the correction through.
func TestNotesAreReplacedByDefault(t *testing.T) {
	f := setup(t, true)
	f.fake.release = &github.Release{ID: 42, TagName: "v1.0.0", Body: "stale notes"}

	result, err := publish.Run(context.Background(), f.opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if f.fake.release.Body != "notes" {
		t.Errorf("release body = %q, want the generated notes", f.fake.release.Body)
	}
	if result.AppendedNotes {
		t.Error("AppendedNotes reported for a replacement")
	}
}

// Append is for a description written by hand and then topped up with the
// generated changelog.
func TestNotesCanBeAppended(t *testing.T) {
	f := setup(t, true)
	f.opts.Notes = publish.NotesAppend
	f.fake.release = &github.Release{ID: 42, TagName: "v1.0.0", Body: "a hand-written preamble"}

	result, err := publish.Run(context.Background(), f.opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(f.fake.release.Body, "a hand-written preamble") {
		t.Errorf("existing notes were lost: %q", f.fake.release.Body)
	}
	if !strings.Contains(f.fake.release.Body, "notes") {
		t.Errorf("generated notes were not appended: %q", f.fake.release.Body)
	}
	if !result.AppendedNotes {
		t.Error("AppendedNotes was not reported")
	}
}

// Appending to nothing is just setting.
func TestAppendToEmptyNotesDoesNotAddSeparator(t *testing.T) {
	f := setup(t, true)
	f.opts.Notes = publish.NotesAppend
	f.fake.release = &github.Release{ID: 42, TagName: "v1.0.0", Body: "   "}

	if _, err := publish.Run(context.Background(), f.opts); err != nil {
		t.Fatal(err)
	}
	if f.fake.release.Body != "notes" {
		t.Errorf("release body = %q, want just the generated notes", f.fake.release.Body)
	}
}

// A token that may attach files but not edit the description should still
// attach the files. The assets are the substance of a release.
func TestRefusedEditStillUploadsAssets(t *testing.T) {
	f := setup(t, true)
	f.fake.release = &github.Release{ID: 42, TagName: "v1.0.0", Body: "existing"}
	f.fake.refuseEdits = true

	result, err := publish.Run(context.Background(), f.opts)
	if err != nil {
		t.Fatalf("Run abandoned an upload it was permitted to perform: %v", err)
	}
	if !result.NotesRefused {
		t.Error("NotesRefused was not reported")
	}
	if len(result.Uploaded) != len(f.files) {
		t.Errorf("uploaded %v, want all %d files", result.Uploaded, len(f.files))
	}
	if f.fake.release.Body != "existing" {
		t.Errorf("release body = %q, want it unchanged", f.fake.release.Body)
	}
}

// An error that is not a refusal must still stop the run.
func TestNonPermissionUpdateFailureIsFatal(t *testing.T) {
	f := setup(t, true)
	f.fake.release = &github.Release{ID: 42, TagName: "v1.0.0"}
	f.fake.failEditsWith = http.StatusInternalServerError

	if _, err := publish.Run(context.Background(), f.opts); err == nil {
		t.Error("Run continued past a server error on the release update")
	}
}

func TestNotesAreCorrectedOnResume(t *testing.T) {
	f := setup(t, true)

	if _, err := publish.Run(context.Background(), f.opts); err != nil {
		t.Fatal(err)
	}

	f.opts.Release.Body = "corrected notes"
	if _, err := publish.Run(context.Background(), f.opts); err != nil {
		t.Fatal(err)
	}

	if f.fake.release.Body != "corrected notes" {
		t.Errorf("release body = %q, want the corrected notes", f.fake.release.Body)
	}
	if f.fake.updated == 0 {
		t.Error("the release was never updated")
	}
}

func TestUploadFailureIsReported(t *testing.T) {
	f := setup(t, true)
	f.fake.failNext = f.opts.Files[0]

	_, err := publish.Run(context.Background(), f.opts)
	if err == nil {
		t.Fatal("Run succeeded despite a failed upload")
	}
	if !strings.Contains(err.Error(), "upstream hiccup") {
		t.Errorf("error does not carry the server's message: %v", err)
	}
}

func TestMissingFileIsReported(t *testing.T) {
	f := setup(t, true)
	f.opts.Files = append(f.opts.Files, "not-built.tar.gz")

	if _, err := publish.Run(context.Background(), f.opts); err == nil {
		t.Error("Run succeeded with a file that does not exist")
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
