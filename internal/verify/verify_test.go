package verify_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/danielriddell21/letsgo/internal/audit"
	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/pgpwords"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/randomart"
	"github.com/danielriddell21/letsgo/internal/release"
	"github.com/danielriddell21/letsgo/internal/verify"
)

const mainGo = `package main

import (
	"fmt"
	"os"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Printf("demo %s (%s) built %s\n", version, commit, date)
	}
}
`

// published is a release that actually exists: built by the real pipeline,
// then served back so verification has something genuine to check.
type published struct {
	dir    string // the repository
	dist   string // the built artifacts
	result *release.Result
	assets map[string]int64 // name -> id
}

// writeFiles creates each named file (with its content) under dir, making
// parent directories as needed.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// gitInit commits every file already written under dir as one commit, with a
// fixed author/committer identity and timestamp so a rebuild's output is
// reproducible, then applies each tag to that commit.
func gitInit(t *testing.T, dir string, tags ...string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_AUTHOR_DATE=2024-03-15T12:30:45Z", "GIT_COMMITTER_DATE=2024-03-15T12:30:45Z",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")

	// Deliberately hostile, and deliberately not corrected with
	// .gitattributes: this is git's default on Windows, and it rewrites line
	// endings on checkout. A rebuild from such a checkout compiles different
	// source than the release did, so verification fails with no defect behind
	// it. Configuring it here exercises the guard on every platform rather
	// than only on the one where it bites.
	run("config", "core.autocrlf", "true")

	run("add", ".")
	run("commit", "-q", "-m", "feat: first")
	for _, tag := range tags {
		run("tag", tag)
	}
}

func buildRelease(t *testing.T) *published {
	t.Helper()
	return buildReleaseWithConfig(t, "build "+gobuild.Host().String()+"\n")
}

// buildReleaseWithConfig is buildRelease with letsgo.mod's contents as a
// parameter, so a test can exercise a directive (like `disable`) without
// duplicating the fixture around it.
func buildReleaseWithConfig(t *testing.T, letsgoMod string) *published {
	t.Helper()
	dir := t.TempDir()

	writeFiles(t, dir, map[string]string{
		"go.mod":     "module example.com/demo\n\ngo 1.24\n",
		"main.go":    mainGo,
		"README.md":  "# demo\n",
		"letsgo.mod": letsgoMod,
	})
	gitInit(t, dir, "v1.2.3")

	p, err := plan.Resolve(context.Background(), plan.Options{Dir: dir})
	if err != nil || !p.OK() {
		t.Fatalf("plan: %v %+v", err, p.Checks)
	}

	dist := t.TempDir()
	result, err := release.Build(context.Background(), p, dist, "test", nil, nil)
	if err != nil {
		t.Fatalf("release.Build: %v", err)
	}
	return &published{dir: dir, dist: dist, result: result, assets: map[string]int64{}}
}

// serve exposes the built release through enough of the API for verification.
// corrupt names an asset whose reported digest should be wrong.
// assetsFrom builds the asset list a release would publish for files, whose
// bytes are read from dist, numbering IDs from startID+1. corrupt names an
// asset whose reported digest should be wrong, or "" for none.
func assetsFrom(t *testing.T, dist string, files []string, startID int64, corrupt string) ([]github.Asset, map[int64]string) {
	t.Helper()
	var assets []github.Asset
	byID := map[int64]string{}
	id := startID
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(dist, name))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256Of(data)
		if name == corrupt {
			digest = strings.Repeat("0", 64)
		}
		id++
		byID[id] = name
		assets = append(assets, github.Asset{
			ID: id, Name: name, Size: int64(len(data)), Digest: "sha256:" + digest,
		})
	}
	return assets, byID
}

// serveAsset answers a release-asset download by looking up which directory
// and file the request's asset ID names.
func serveAsset(t *testing.T, w http.ResponseWriter, r *http.Request, lookup func(id int64) (dist, name string, ok bool)) {
	t.Helper()
	idText := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	id, _ := strconv.ParseInt(idText, 10, 64)
	dist, name, ok := lookup(id)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	data, err := os.ReadFile(filepath.Join(dist, name))
	if err != nil {
		t.Error(err)
	}
	_, _ = w.Write(data)
}

func serveNoAttestations(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"attestations": []any{}})
}

func (p *published) serve(t *testing.T, corrupt string) *github.Client {
	t.Helper()
	return p.serveWith(t, corrupt, "", nil)
}

// serveWith is serve, optionally also serving one more asset (extraName,
// extraData) by literal bytes rather than a file under dist — for the
// audit.json consumer checks (AU-9, AU-10), which have no counterpart
// among the build's own published files.
func (p *published) serveWith(t *testing.T, corrupt, extraName string, extraData []byte) *github.Client {
	t.Helper()

	assets, byID := assetsFrom(t, p.dist, p.result.Files, 100, corrupt)
	for id, name := range byID {
		p.assets[name] = id
	}

	const extraID = 999
	if extraName != "" {
		assets = append(assets, github.Asset{
			ID: extraID, Name: extraName, Size: int64(len(extraData)), Digest: "sha256:" + sha256Of(extraData),
		})
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/releases/tags/") || strings.HasSuffix(r.URL.Path, "/releases/latest"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(github.Release{
				ID: 1, TagName: "v1.2.3", Assets: assets,
			})

		case extraName != "" && strings.HasSuffix(r.URL.Path, fmt.Sprintf("/releases/assets/%d", extraID)):
			_, _ = w.Write(extraData)

		case strings.Contains(r.URL.Path, "/releases/assets/"):
			serveAsset(t, w, r, func(id int64) (string, string, bool) {
				name, ok := byID[id]
				return p.dist, name, ok
			})

		case strings.Contains(r.URL.Path, "/attestations/"):
			serveNoAttestations(w)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	client := github.New("token")
	client.SetEndpoints(server.URL, server.URL)
	return client
}

func run(t *testing.T, p *published, o verify.Options) *verify.Result {
	t.Helper()
	if o.Client == nil {
		o.Client = p.serve(t, "")
	}
	o.Repo = github.Repo{Owner: "you", Name: "demo"}
	o.WorkDir = t.TempDir()
	o.GoBin = goBin(t)
	o.GitBin = "git"

	result, err := verify.Run(context.Background(), o)
	if err != nil {
		t.Fatalf("verify.Run: %v", err)
	}
	return result
}

func find(t *testing.T, r *verify.Result, name string) verify.Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check named %q in %+v", name, r.Checks)
	return verify.Check{}
}

// The whole loop: build a release, publish it, rebuild it, and find the same
// bytes. If this does not hold, nothing else in the tool means very much.
func TestVerifyReproducesARealRelease(t *testing.T) {
	p := buildRelease(t)
	result := run(t, p, verify.Options{Tag: "v1.2.3", Dir: p.dir})

	if !result.OK() {
		t.Fatalf("a freshly built release did not verify: %+v", result.Checks)
	}
	if c := find(t, result, "rebuild"); c.Status != verify.Pass {
		t.Errorf("rebuild = %+v", c)
	}
	if !strings.Contains(result.SourceFrom, "local checkout") {
		t.Errorf("SourceFrom = %q, want the local checkout", result.SourceFrom)
	}
	assertArtifacts(t, result, verify.Pass)
}

// assertArtifacts requires every artifact to carry want, and there to be some.
func assertArtifacts(t *testing.T, result *verify.Result, want verify.Status) {
	t.Helper()
	if len(result.Artifacts) == 0 {
		t.Fatal("no per-artifact results")
	}
	for _, a := range result.Artifacts {
		if a.Status != want {
			t.Errorf("artifact %s = %s, want %s", a.Name, a.Status, want)
		}
	}
}

// Without a checkout the release's own source archive is used, and the report
// must say so: it establishes internal consistency, not provenance from a
// repository anyone can read.
func TestVerifyFallsBackToThePublishedSource(t *testing.T) {
	p := buildRelease(t)
	result := run(t, p, verify.Options{Tag: "v1.2.3"}) // no Dir

	if !result.OK() {
		t.Fatalf("verification failed: %+v", result.Checks)
	}
	if !strings.Contains(result.SourceFrom, "source archive") {
		t.Errorf("SourceFrom = %q, want the published source archive", result.SourceFrom)
	}
}

// An asset that does not match what the release describes is the case this
// exists to catch.
func TestVerifyDetectsATamperedAsset(t *testing.T) {
	p := buildRelease(t)
	corrupted := p.result.Artifacts[0].Archive

	result := run(t, p, verify.Options{
		GitBin: "git",
		Tag:    "v1.2.3", Dir: p.dir, SkipRebuild: true,
		Client: p.serve(t, corrupted),
	})

	if result.OK() {
		t.Fatal("a mismatched asset digest verified")
	}
	c := find(t, result, "published assets")
	if c.Status != verify.Fail || !strings.Contains(c.Detail, corrupted) {
		t.Errorf("published assets = %+v, want a failure naming %s", c, corrupted)
	}
	failed := 0
	for _, a := range result.Artifacts {
		if a.Status == verify.Fail {
			failed++
		}
	}
	if failed != 1 {
		t.Errorf("%d artifacts failed, want exactly the tampered one: %+v", failed, result.Artifacts)
	}
}

func TestVerifySkipsRebuildWhenAsked(t *testing.T) {
	p := buildRelease(t)
	result := run(t, p, verify.Options{Tag: "v1.2.3", Dir: p.dir, SkipRebuild: true})

	if c := find(t, result, "rebuild"); c.Status != verify.Skip {
		t.Errorf("rebuild = %+v, want skip", c)
	}
	if !result.OK() {
		t.Errorf("skipping the rebuild should not fail verification: %+v", result.Checks)
	}
	assertArtifacts(t, result, verify.Skip)
}

// A release with no provenance is still verifiable; it carries one guarantee
// fewer, which is worth reporting and not worth failing.
func TestVerifyReportsMissingProvenanceWithoutFailing(t *testing.T) {
	p := buildRelease(t)
	result := run(t, p, verify.Options{Tag: "v1.2.3", Dir: p.dir, SkipRebuild: true})

	if c := find(t, result, "provenance"); c.Status != verify.Warn {
		t.Errorf("provenance = %+v, want warn", c)
	}
	if !result.OK() {
		t.Error("missing provenance failed the verification")
	}
}

// Only releases that published a manifest can be verified, and the reason
// has to be legible.
func TestVerifyNeedsAManifest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(github.Release{ID: 1, TagName: "v9.9.9"})
	}))
	defer server.Close()

	client := github.New("token")
	client.SetEndpoints(server.URL, server.URL)

	_, err := verify.Run(context.Background(), verify.Options{
		GitBin: "git",
		Client: client, Repo: github.Repo{Owner: "you", Name: "demo"},
		Tag: "v9.9.9", WorkDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("a release with no manifest verified")
	}
	if !strings.Contains(err.Error(), manifest.FileName) {
		t.Errorf("error does not explain what is missing: %v", err)
	}
}

// With no tag given, a scoped module resolves the newest release within its
// own prefix, not the repository's overall latest — the same distinction
// runYank draws before picking a previous release.
func TestVerifyWithNoTagIsScopedToThePrefix(t *testing.T) {
	const prefix = "services/api/"
	manifestData := []byte(fmt.Sprintf(`{"schema":%d}`, manifest.Schema))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tags"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]github.Tag{
				{Name: "v9.9.9"},       // a higher, unscoped decoy
				{Name: "other/v8.0.0"}, // a different module's scope
				{Name: "services/api/v1.0.0"},
				{Name: "services/api/v1.2.3"}, // the scoped winner
			})

		case strings.Contains(r.URL.Path, "/releases/tags/"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(github.Release{
				ID: 1, TagName: "services/api/v1.2.3",
				Assets: []github.Asset{{ID: 1, Name: manifest.FileName, Size: int64(len(manifestData))}},
			})

		case strings.Contains(r.URL.Path, "/releases/assets/"):
			_, _ = w.Write(manifestData)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := github.New("token")
	client.SetEndpoints(server.URL, server.URL)

	result, err := verify.Run(context.Background(), verify.Options{
		GitBin: "git",
		Client: client, Repo: github.Repo{Owner: "you", Name: "demo"},
		Prefix: prefix, WorkDir: t.TempDir(), SkipRebuild: true,
	})
	if err != nil {
		t.Fatalf("verify.Run: %v", err)
	}
	if result.Tag != "services/api/v1.2.3" {
		t.Errorf("Tag = %q, want the scoped module's own latest release, not the repository's overall latest", result.Tag)
	}
}

// With no tag and no prefix, the most recent release comes from the forge's
// own "latest release" endpoint rather than a tag listing: a root module has
// no scope to filter tags by, so there is nothing for the tag-listing path to
// add.
func TestVerifyWithNoTagAndNoPrefixUsesLatestRelease(t *testing.T) {
	p := buildRelease(t)
	result := run(t, p, verify.Options{Dir: p.dir, SkipRebuild: true})

	if result.Tag != "v1.2.3" {
		t.Errorf("Tag = %q, want v1.2.3 from the latest-release endpoint", result.Tag)
	}
}

// A prefix that matches no tag means the module has no release yet, and that
// has to surface as "no releases" rather than a nil pointer.
func TestVerifyScopedToAPrefixWithNoMatchingTagHasNoReleases(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tags") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]github.Tag{{Name: "other/v1.0.0"}})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := github.New("token")
	client.SetEndpoints(server.URL, server.URL)

	_, err := verify.Run(context.Background(), verify.Options{
		GitBin: "git",
		Client: client, Repo: github.Repo{Owner: "you", Name: "demo"},
		Prefix: "services/api/", WorkDir: t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "has no releases") {
		t.Errorf("err = %v, want \"has no releases\" for a prefix with no matching tag", err)
	}
}

// A forge error listing tags must surface, not be swallowed as "no releases".
func TestVerifyScopedToAPrefixSurfacesATagsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := github.New("token")
	client.SetEndpoints(server.URL, server.URL)

	_, err := verify.Run(context.Background(), verify.Options{
		GitBin: "git",
		Client: client, Repo: github.Repo{Owner: "you", Name: "demo"},
		Prefix: "services/api/", WorkDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("a failed tag listing verified")
	}
	if strings.Contains(err.Error(), "has no releases") {
		t.Errorf("err = %v, want the underlying tags error, not \"has no releases\"", err)
	}
}

func TestVerifyRejectsAnUnknownSchema(t *testing.T) {
	if _, err := manifest.Decode([]byte(`{"schema":99}`)); err == nil {
		t.Error("an unknown schema was accepted")
	}
}

// A monorepo carries a root module and a nested one, each with its own go.mod
// and its own tag. Both are built and released for real, then verified
// end-to-end — rebuild included — to prove that verifying one never leans on
// the other's tag, version, or source directory.
func TestMonorepoRootAndNestedModuleReleaseAndVerifyIndependently(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "services/api")

	writeFiles(t, dir, map[string]string{
		"go.mod":                  "module example.com/demo\n\ngo 1.24\n",
		"main.go":                 mainGo,
		"README.md":               "# demo\n",
		"letsgo.mod":              "build " + gobuild.Host().String() + "\n",
		"services/api/go.mod":     "module example.com/demo/services/api\n\ngo 1.24\n",
		"services/api/main.go":    mainGo,
		"services/api/letsgo.mod": "build " + gobuild.Host().String() + "\n",
	})
	gitInit(t, dir, "v1.0.0", "services/api/v1.5.0")

	build := func(modDir string) (*release.Result, string) {
		t.Helper()
		p, err := plan.Resolve(context.Background(), plan.Options{Dir: modDir})
		if err != nil || !p.OK() {
			t.Fatalf("plan(%s): %v %+v", modDir, err, p.Checks)
		}
		dist := t.TempDir()
		result, err := release.Build(context.Background(), p, dist, "test", nil, nil)
		if err != nil {
			t.Fatalf("release.Build(%s): %v", modDir, err)
		}
		return result, dist
	}

	rootResult, rootDist := build(dir)
	apiResult, apiDist := build(nested)

	if rootResult.Manifest.TagPrefix != "" {
		t.Errorf("root TagPrefix = %q, want empty", rootResult.Manifest.TagPrefix)
	}
	if apiResult.Manifest.TagPrefix != "services/api/" {
		t.Errorf("api TagPrefix = %q, want %q", apiResult.Manifest.TagPrefix, "services/api/")
	}
	if apiResult.Manifest.ModuleDir != "services/api" {
		t.Errorf("api ModuleDir = %q, want %q", apiResult.Manifest.ModuleDir, "services/api")
	}

	// assets, keyed by ID across both releases, since a real forge has one ID
	// space for the whole repository, not one per release.
	rootAssets, rootByID := assetsFrom(t, rootDist, rootResult.Files, 100, "")
	apiAssets, apiByID := assetsFrom(t, apiDist, apiResult.Files, 200, "")

	dists := map[string]string{"v1.0.0": rootDist, "services/api/v1.5.0": apiDist}
	releaseFor := func(tag string) github.Release {
		if tag == "v1.0.0" {
			return github.Release{ID: 1, TagName: tag, Assets: rootAssets}
		}
		return github.Release{ID: 2, TagName: tag, Assets: apiAssets}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tags"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]github.Tag{{Name: "v1.0.0"}, {Name: "services/api/v1.5.0"}})

		case strings.Contains(r.URL.Path, "/releases/tags/"):
			idx := strings.LastIndex(r.URL.Path, "/releases/tags/")
			tag := r.URL.Path[idx+len("/releases/tags/"):]
			if _, ok := dists[tag]; !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(releaseFor(tag))

		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(releaseFor("v1.0.0"))

		case strings.Contains(r.URL.Path, "/releases/assets/"):
			serveAsset(t, w, r, func(id int64) (string, string, bool) {
				if name, ok := rootByID[id]; ok {
					return rootDist, name, true
				}
				name, ok := apiByID[id]
				return apiDist, name, ok
			})

		case strings.Contains(r.URL.Path, "/attestations/"):
			serveNoAttestations(w)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	client := github.New("token")
	client.SetEndpoints(server.URL, server.URL)
	repo := github.Repo{Owner: "you", Name: "demo"}

	rootVerify, err := verify.Run(context.Background(), verify.Options{
		GitBin: "git",
		GoBin:  goBin(t),
		Client: client, Repo: repo, Tag: "v1.0.0", Dir: dir, WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("verify root: %v", err)
	}
	if !rootVerify.OK() {
		t.Errorf("root release did not verify: %+v", rootVerify.Checks)
	}

	apiVerify, err := verify.Run(context.Background(), verify.Options{
		GitBin: "git",
		GoBin:  goBin(t),
		Client: client, Repo: repo, Tag: "services/api/v1.5.0", Dir: nested, WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("verify api: %v", err)
	}
	if !apiVerify.OK() {
		t.Errorf("nested release did not verify: %+v", apiVerify.Checks)
	}

	// With no tag given, each scope must resolve its own release, not the
	// other's — the root's tag never leaks into the nested prefix, and the
	// nested tag (however new) never outranks the root's own "latest".
	noTagAPI, err := verify.Run(context.Background(), verify.Options{
		GitBin: "git",
		GoBin:  goBin(t),
		Client: client, Repo: repo, Prefix: "services/api/", SkipRebuild: true, WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("verify api with no tag: %v", err)
	}
	if noTagAPI.Tag != "services/api/v1.5.0" {
		t.Errorf("api Tag = %q, want the nested module's own release", noTagAPI.Tag)
	}

	noTagRoot, err := verify.Run(context.Background(), verify.Options{
		GitBin: "git",
		GoBin:  goBin(t),
		Client: client, Repo: repo, Dir: dir, SkipRebuild: true, WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("verify root with no tag: %v", err)
	}
	if noTagRoot.Tag != "v1.0.0" {
		t.Errorf("root Tag = %q, want the root's own release", noTagRoot.Tag)
	}
}

// A release built with a feature disabled must report it, in the same
// vocabulary plan itself used to decide it.
func TestVerifyReportsDisabledFeatures(t *testing.T) {
	p := buildReleaseWithConfig(t, "build "+gobuild.Host().String()+"\ndisable sbom\n")
	result := run(t, p, verify.Options{Tag: "v1.2.3", Dir: p.dir, SkipRebuild: true})

	c := find(t, result, "features")
	if c.Status != verify.Pass || !strings.Contains(c.Detail, "disabled: sbom") {
		t.Errorf("features = %+v, want a pass naming sbom disabled", c)
	}
}

// A release with every feature at its default carries no Features record,
// and verify must not invent a line to report.
func TestVerifyOmitsFeaturesWhenNoneAreSet(t *testing.T) {
	p := buildRelease(t)
	result := run(t, p, verify.Options{Tag: "v1.2.3", Dir: p.dir, SkipRebuild: true})

	for _, c := range result.Checks {
		if c.Name == "features" {
			t.Errorf("features = %+v, want no features check when none are set", c)
		}
	}
}

// A release audited as affected still verifies (AU-9): a later vulndb
// finding something is not the release's own fault, so it surfaces as an
// informational pass naming the finding (AU-10), not a failure.
func TestVerifyReportsAnAffectedAudit(t *testing.T) {
	p := buildRelease(t)
	record := audit.Record{Schema: audit.Schema, Tag: "v1.2.3", Audits: []audit.Entry{
		{At: time.Now(), Vulndb: "2026-09-24", Status: audit.Affected, Findings: []audit.Finding{{ID: "GO-2026-1234", Module: "example.com/vuln"}}},
	}}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}

	result := run(t, p, verify.Options{
		GitBin: "git",
		GoBin:  goBin(t),
		Tag:    "v1.2.3", Dir: p.dir, SkipRebuild: true,
		Client: p.serveWith(t, "", "audit.json", data),
	})

	if !result.OK() {
		t.Errorf("an affected audit failed verification: %+v", result.Checks)
	}
	c := find(t, result, "audit")
	if c.Status != verify.Pass || !strings.Contains(c.Detail, "affected by GO-2026-1234 (as of 2026-09-24)") {
		t.Errorf("audit = %+v, want a pass naming the finding", c)
	}
}

// A clean audit gets its own, shorter line.
func TestVerifyReportsACleanAudit(t *testing.T) {
	p := buildRelease(t)
	record := audit.Record{Schema: audit.Schema, Tag: "v1.2.3", Audits: []audit.Entry{
		{At: time.Now(), Vulndb: "2026-09-24", Status: audit.Clean},
	}}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}

	result := run(t, p, verify.Options{
		GitBin: "git",
		GoBin:  goBin(t),
		Tag:    "v1.2.3", Dir: p.dir, SkipRebuild: true,
		Client: p.serveWith(t, "", "audit.json", data),
	})

	c := find(t, result, "audit")
	if c.Status != verify.Pass || c.Detail != "clean (as of 2026-09-24)" {
		t.Errorf("audit = %+v, want a clean pass", c)
	}
}

// A release with no audit.json is unaudited, not a failure: verify must not
// invent a line to report.
func TestVerifyOmitsAuditWhenNoneExists(t *testing.T) {
	p := buildRelease(t)
	result := run(t, p, verify.Options{Tag: "v1.2.3", Dir: p.dir, SkipRebuild: true})

	for _, c := range result.Checks {
		if c.Name == "audit" {
			t.Errorf("audit = %+v, want no audit check when audit.json is absent", c)
		}
	}
}

// A release applied from a plan records it, and verify prints the record and
// checks the attached plan is the one it names (PA-12).
func stampedPlan(t *testing.T, p *published) {
	t.Helper()
	if err := release.StampPlan(p.result, []byte(`{"kind":"release"}`), "2026-09-30T10:00:00Z", "0.31.0"); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyReportsThePlanAReleaseWasAppliedFrom(t *testing.T) {
	p := buildRelease(t)
	stampedPlan(t, p)
	result := run(t, p, verify.Options{Tag: "v1.2.3", Dir: p.dir, SkipRebuild: true})

	c := find(t, result, "plan")
	if c.Status != verify.Pass || !strings.Contains(c.Detail, "made 2026-09-30T10:00:00Z by letsgo 0.31.0") {
		t.Errorf("plan = %+v, want a pass naming when and by what the plan was made", c)
	}
	if !result.OK() {
		t.Errorf("a stamped release failed verification: %+v", result.Checks)
	}
}

func TestVerifyFailsWhenTheAttachedPlanIsNotTheRecordedOne(t *testing.T) {
	p := buildRelease(t)
	stampedPlan(t, p)
	if err := os.WriteFile(filepath.Join(p.dist, release.PlanFileName), []byte(`{"kind":"other"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	result := run(t, p, verify.Options{Tag: "v1.2.3", Dir: p.dir, SkipRebuild: true})

	if c := find(t, result, "plan"); c.Status != verify.Fail || !strings.Contains(c.Detail, "the attached") {
		t.Errorf("plan = %+v, want a fail for a plan that does not match its record", c)
	}
}

func TestVerifyFailsWhenTheRecordedPlanIsNotAttached(t *testing.T) {
	p := buildRelease(t)
	stampedPlan(t, p)
	files := p.result.Files[:0:0]
	for _, name := range p.result.Files {
		if name != release.PlanFileName {
			files = append(files, name)
		}
	}
	p.result.Files = files
	result := run(t, p, verify.Options{Tag: "v1.2.3", Dir: p.dir, SkipRebuild: true})

	if c := find(t, result, "plan"); c.Status != verify.Fail || !strings.Contains(c.Detail, "is not attached") {
		t.Errorf("plan = %+v, want a fail for a missing plan", c)
	}
}

// A release made without a plan has no record to report.
func TestVerifyOmitsThePlanWhenNoneWasUsed(t *testing.T) {
	p := buildRelease(t)
	result := run(t, p, verify.Options{Tag: "v1.2.3", Dir: p.dir, SkipRebuild: true})

	for _, c := range result.Checks {
		if c.Name == "plan" {
			t.Errorf("plan = %+v, want no plan check for a release made without one", c)
		}
	}
}

func sha256Of(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// JSON is Result's wire form for `letsgo verify --json`: schema-versioned,
// carrying the same checks Report writes as text.
func TestResultJSON(t *testing.T) {
	r := &verify.Result{
		Tag:        "v1.2.3",
		SourceFrom: "local checkout",
		Checks: []verify.Check{
			{Name: "manifest", Status: verify.Pass, Detail: "ok"},
			{Name: "sbom", Status: verify.Fail, Detail: "missing"},
		},
	}

	data, err := r.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}

	for _, want := range []string{
		`"schema": 1`,
		`"tag": "v1.2.3"`,
		`"sourceFrom": "local checkout"`,
		`"name": "manifest"`,
		`"status": "pass"`,
		`"detail": "ok"`,
		`"name": "sbom"`,
		`"status": "fail"`,
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("JSON() = %s, want it to contain %q", data, want)
		}
	}
}

func TestResultJSONOmitsAnEmptySourceFrom(t *testing.T) {
	r := &verify.Result{Tag: "v1.2.3"}

	data, err := r.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if strings.Contains(string(data), `"sourceFrom"`) {
		t.Errorf("JSON() = %s, want no sourceFrom field when empty", data)
	}
}

// RA-4: a pass ends with the fingerprint of the published manifest.
func TestVerifyPrintsTheFingerprintAfterAPass(t *testing.T) {
	p := buildRelease(t)
	result := run(t, p, verify.Options{Tag: "v1.2.3", Dir: p.dir, SkipRebuild: true})

	data, err := os.ReadFile(filepath.Join(p.dist, manifest.FileName))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	want := randomart.Render(sum[:], "letsgo v1.2.3") + "\nsha256:" + hex.EncodeToString(sum[:]) + "\n"

	var out strings.Builder
	result.Report(&out)
	if !strings.HasSuffix(out.String(), "\n"+want) {
		t.Errorf("report does not end with the fingerprint:\n%s", out.String())
	}
}

// RA-5: a failure prints no art.
func TestVerifyPrintsNoFingerprintOnAFailure(t *testing.T) {
	p := buildRelease(t)
	result := run(t, p, verify.Options{
		GitBin: "git",
		GoBin:  goBin(t),
		Tag:    "v1.2.3", Dir: p.dir, SkipRebuild: true,
		Client: p.serve(t, p.result.Artifacts[0].Archive),
	})

	var out strings.Builder
	result.Report(&out)
	if strings.Contains(out.String(), "[SHA256]") || strings.Contains(out.String(), "sha256:") {
		t.Errorf("a failing report printed a fingerprint:\n%s", out.String())
	}
}

// PW-3, PW-4, PW-6: a pass reads out all 32 bytes as words, the same way the
// release notes do, and the JSON carries them too.
func TestVerifyReadsTheManifestDigestAloudAfterAPass(t *testing.T) {
	p := buildRelease(t)
	result := run(t, p, verify.Options{Tag: "v1.2.3", Dir: p.dir, SkipRebuild: true})

	data, err := os.ReadFile(filepath.Join(p.dist, manifest.FileName))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	words := pgpwords.Encode(sum[:])

	var out strings.Builder
	result.ReportWords(&out)
	want := "\n  manifest sha256, read aloud:\n" + pgpwords.Rows(words)
	if out.String() != want {
		t.Errorf("ReportWords = %q, want %q", out.String(), want)
	}

	if got := result.Words(); len(got) != 32 {
		t.Errorf("Words has %d words, want 32", len(got))
	}
	js, err := result.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(js), `"manifest_words"`) || !strings.Contains(string(js), words[0]) {
		t.Errorf("JSON = %s, want manifest_words", js)
	}
}

// PW-5: nobody should read out a hash that did not verify.
func TestVerifyReadsNoWordsOnAFailure(t *testing.T) {
	p := buildRelease(t)
	result := run(t, p, verify.Options{
		GitBin: "git",
		GoBin:  goBin(t),
		Tag:    "v1.2.3", Dir: p.dir, SkipRebuild: true,
		Client: p.serve(t, p.result.Artifacts[0].Archive),
	})

	var out strings.Builder
	result.ReportWords(&out)
	if out.Len() != 0 || result.Words() != nil {
		t.Errorf("a failing verify read out %q, %v", out.String(), result.Words())
	}
	js, err := result.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(js), "manifest_words") {
		t.Errorf("JSON = %s, want no manifest_words", js)
	}
}

func goBin(t *testing.T) string {
	t.Helper()
	bin, _, err := gobuild.Toolchain(nil)
	if err != nil {
		t.Skipf("no go command: %v", err)
	}
	return bin
}
