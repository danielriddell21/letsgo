package audit_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/danielriddell21/letsgo/internal/audit"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/release"
	"github.com/danielriddell21/letsgo/internal/releasetest"
)

// published is a release built by the real pipeline, so its source archive
// and manifest digest are exactly what audit has to check against — the
// same fixture shape internal/verify's tests use, trimmed to what audit
// itself reads (the tags and asset-download endpoints; no attestations).
type published struct {
	tag    string
	dist   string
	result *release.Result
}

func buildRelease(t *testing.T) *published {
	t.Helper()
	return buildReleaseTagged(t, "v1.2.3")
}

// demoModulePath gives the fixture's module a "/vN" suffix for a major 2+
// tag, as Go's own module versioning rules require.
func demoModulePath(tag string) string {
	major, _, _ := strings.Cut(strings.TrimPrefix(tag, "v"), ".")
	if major == "0" || major == "1" {
		return "example.com/demo"
	}
	return "example.com/demo/v" + major
}

// buildReleaseTagged is buildRelease with the tag as a parameter, for tests
// that need more than one real release (RunAll's grouping-by-major logic).
func buildReleaseTagged(t *testing.T, tag string) *published {
	t.Helper()
	dist, result := releasetest.Build(t, demoModulePath(tag), tag)
	return &published{tag: tag, dist: dist, result: result}
}

// served is one asset the fake forge is currently holding, keyed by ID.
// audit.json starts absent and is added/replaced/removed by the same
// upload/delete calls a real release-time publish or a later audit uses,
// so the fake has to track this state rather than serve it statically.
type served struct {
	name string
	data []byte
}

// controls lets a test break one specific request the fake forge would
// otherwise serve normally, to drive audit's own error-handling paths
// (a download, delete, or upload that the real API can fail on) without a
// second, bespoke server.
type controls struct {
	mu sync.Mutex

	failDownload string // asset name: GET its bytes 500s instead
	corrupt      string // asset name: GET returns this instead of its real bytes
	failDelete   string // asset name: DELETE 500s instead of removing it
	failUpload   string // asset name: POST (upload) 500s instead of storing it
}

// serve exposes the built release through enough of the API for audit: the
// tag lookup, asset downloads, and asset upload/delete (so a second
// audit.Run against the same server sees the first run's audit.json).
// tamper serves corrupted bytes for the named asset, so its digest no
// longer matches what the manifest recorded — the "tampered source"
// acceptance scenario. audit checks a downloaded asset against the
// manifest's own digest, not against what the forge reports for it, so
// corrupting only the reported digest (as verify's own fixture does) would
// not exercise this path. The returned *controls lets a test inject a
// failure into a later request against the same server.
func (p *published) serve(t *testing.T, tamper string) (*github.Client, *controls) {
	t.Helper()

	ctl := &controls{}
	byID := map[int64]served{}
	nextID := int64(100)
	for _, name := range p.result.Files {
		data, err := os.ReadFile(filepath.Join(p.dist, name))
		if err != nil {
			t.Fatal(err)
		}
		nextID++
		byID[nextID] = served{name: name, data: data}
	}

	assetsLocked := func() []github.Asset {
		var assets []github.Asset
		for id, s := range byID {
			assets = append(assets, github.Asset{ID: id, Name: s.name, Size: int64(len(s.data)), Digest: "sha256:" + sha256Hex(s.data)})
		}
		return assets
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctl.mu.Lock()
		defer ctl.mu.Unlock()

		switch {
		case strings.Contains(r.URL.Path, "/releases/tags/"):
			if !strings.HasSuffix(r.URL.Path, "/v1.2.3") {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(github.Release{ID: 1, TagName: "v1.2.3", Assets: assetsLocked()})

		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/releases/") && strings.HasSuffix(r.URL.Path, "/assets"):
			name := r.URL.Query().Get("name")
			if name == ctl.failUpload {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			data, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			nextID++
			byID[nextID] = served{name: name, data: data}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(github.Asset{ID: nextID, Name: name, Size: int64(len(data)), Digest: "sha256:" + sha256Hex(data)})

		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/releases/assets/"):
			idText := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			assetID, _ := strconv.ParseInt(idText, 10, 64)
			if byID[assetID].name == ctl.failDelete {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			delete(byID, assetID)
			w.WriteHeader(http.StatusNoContent)

		case strings.Contains(r.URL.Path, "/releases/assets/"):
			idText := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			assetID, _ := strconv.ParseInt(idText, 10, 64)
			s, ok := byID[assetID]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if s.name == ctl.failDownload {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if s.name == ctl.corrupt {
				_, _ = w.Write([]byte("not valid json"))
				return
			}
			if s.name == tamper {
				_, _ = w.Write([]byte("not the real archive"))
				return
			}
			_, _ = w.Write(s.data)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	client := github.New("token")
	client.SetEndpoints(server.URL, server.URL)
	return client, ctl
}

// fakeGovulncheck puts a script named govulncheck ahead of PATH that prints
// out (govulncheck's own JSON stream) regardless of its arguments, so tests
// can drive audit.Run without a real vulnerability database.
func fakeGovulncheck(t *testing.T, out string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake tool is a shell script")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\ncat <<'EOF'\n" + out + "\nEOF\n"
	path := filepath.Join(dir, "govulncheck")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

const cleanOutput = `{"config":{"scanner_version":"v1.1.4","db_last_modified":"2026-09-23T00:00:00Z"}}`

const affectedOutput = `{"config":{"scanner_version":"v1.1.4","db_last_modified":"2026-09-23T00:00:00Z"}}
{"finding":{"osv":"GO-2026-1234","fixed_version":"v0.31.0","trace":[{"module":"golang.org/x/net","package":"golang.org/x/net/http2","function":"readFrame"}]}}`

func TestRunReportsACleanRelease(t *testing.T) {
	fakeGovulncheck(t, cleanOutput)
	p := buildRelease(t)
	client, _ := p.serve(t, "")

	result, err := audit.Run(context.Background(), audit.Options{
		Client: client, Repo: github.Repo{Owner: "you", Name: "demo"},
		Tag: "v1.2.3", WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("audit.Run: %v", err)
	}
	if result.Entry.Status != audit.Clean {
		t.Errorf("status = %s, want clean", result.Entry.Status)
	}
	if result.Entry.Vulndb != "2026-09-23" || result.Entry.Govulncheck != "v1.1.4" {
		t.Errorf("entry = %+v", result.Entry)
	}
	if len(result.Entry.Findings) != 0 {
		t.Errorf("findings = %+v, want none", result.Entry.Findings)
	}
	if !result.Recorded {
		t.Error("Recorded = false, want true for a release's first audit")
	}
}

func TestRunReportsAnAffectedRelease(t *testing.T) {
	fakeGovulncheck(t, affectedOutput)
	p := buildRelease(t)
	client, _ := p.serve(t, "")

	result, err := audit.Run(context.Background(), audit.Options{
		Client: client, Repo: github.Repo{Owner: "you", Name: "demo"},
		Tag: "v1.2.3", WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("audit.Run: %v", err)
	}
	if result.Entry.Status != audit.Affected {
		t.Errorf("status = %s, want affected", result.Entry.Status)
	}
	if len(result.Entry.Findings) != 1 || result.Entry.Findings[0].ID != "GO-2026-1234" {
		t.Fatalf("findings = %+v", result.Entry.Findings)
	}
	if result.Entry.Findings[0].Module != "golang.org/x/net" || result.Entry.Findings[0].Fixed != "v0.31.0" {
		t.Errorf("finding = %+v", result.Entry.Findings[0])
	}
	if !result.Recorded {
		t.Error("Recorded = false, want true for a release's first audit")
	}
}

// A second run against an unchanged vulndb (same date, same findings) must
// not append a new entry (AU-6), and must not touch any asset other than
// audit.json (AU-8).
func TestRunSkipsAnUnchangedRerun(t *testing.T) {
	fakeGovulncheck(t, cleanOutput)
	p := buildRelease(t)
	client, _ := p.serve(t, "")
	opts := audit.Options{
		Client: client, Repo: github.Repo{Owner: "you", Name: "demo"},
		Tag: "v1.2.3",
	}

	opts.WorkDir = t.TempDir()
	first, err := audit.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("first audit.Run: %v", err)
	}
	if !first.Recorded {
		t.Fatal("first run was not recorded")
	}

	opts.WorkDir = t.TempDir()
	second, err := audit.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("second audit.Run: %v", err)
	}
	if second.Recorded {
		t.Error("second run recorded a duplicate entry, want it skipped")
	}

	record := fetchRecord(t, client)
	if len(record.Audits) != 1 {
		t.Fatalf("audits = %+v, want exactly 1", record.Audits)
	}

	release, err := client.ReleaseByTag(context.Background(), github.Repo{Owner: "you", Name: "demo"}, "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if len(release.Assets) != len(p.result.Files)+1 {
		t.Fatalf("release has %d assets, want the %d built files plus %s",
			len(release.Assets), len(p.result.Files), audit.FileName)
	}
	for _, name := range p.result.Files {
		asset, ok := release.Asset(name)
		if !ok {
			t.Fatalf("release lost its %s asset", name)
		}
		data, err := os.ReadFile(filepath.Join(p.dist, name))
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := asset.SHA256(); got != sha256Hex(data) {
			t.Errorf("%s digest changed: got %s, want %s", name, got, sha256Hex(data))
		}
	}
}

// A run against a changed vulndb (a new database date, even with the same
// findings) must append, keeping the earlier entry (AU-5).
func TestRunAppendsWhenTheVulndbChanges(t *testing.T) {
	fakeGovulncheck(t, cleanOutput)
	p := buildRelease(t)
	client, _ := p.serve(t, "")
	opts := audit.Options{
		Client: client, Repo: github.Repo{Owner: "you", Name: "demo"},
		Tag: "v1.2.3",
	}

	opts.WorkDir = t.TempDir()
	if _, err := audit.Run(context.Background(), opts); err != nil {
		t.Fatalf("first audit.Run: %v", err)
	}

	fakeGovulncheck(t, laterOutput)
	opts.WorkDir = t.TempDir()
	second, err := audit.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("second audit.Run: %v", err)
	}
	if !second.Recorded {
		t.Error("Recorded = false, want true when the vulndb date changed")
	}

	record := fetchRecord(t, client)
	if len(record.Audits) != 2 {
		t.Fatalf("audits = %+v, want exactly 2", record.Audits)
	}
	if record.Audits[0].Vulndb != "2026-09-23" || record.Audits[1].Vulndb != "2026-09-24" {
		t.Errorf("audits = %+v", record.Audits)
	}
}

// fetchRecord reads back the audit.json the fake forge is currently
// holding for v1.2.3, the same way a later audit.Run would.
func fetchRecord(t *testing.T, client *github.Client) audit.Record {
	t.Helper()
	release, err := client.ReleaseByTag(context.Background(), github.Repo{Owner: "you", Name: "demo"}, "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	asset, ok := release.Asset(audit.FileName)
	if !ok {
		t.Fatalf("release has no %s", audit.FileName)
	}
	data, err := client.DownloadAsset(context.Background(), github.Repo{Owner: "you", Name: "demo"}, asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	var record audit.Record
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	return record
}

// A tampered source archive must fail before govulncheck ever runs: the
// scan would otherwise be of bytes the release never published.
func TestRunFailsOnATamperedSourceArchive(t *testing.T) {
	fakeGovulncheck(t, cleanOutput)
	p := buildRelease(t)
	client, _ := p.serve(t, p.result.Manifest.Source.Archive)

	_, err := audit.Run(context.Background(), audit.Options{
		Client: client,
		Repo:   github.Repo{Owner: "you", Name: "demo"},
		Tag:    "v1.2.3", WorkDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("a tampered source archive was not rejected")
	}
}

func TestRunFailsForAnUnknownTag(t *testing.T) {
	fakeGovulncheck(t, cleanOutput)
	p := buildRelease(t)
	client, _ := p.serve(t, "")

	_, err := audit.Run(context.Background(), audit.Options{
		Client: client, Repo: github.Repo{Owner: "you", Name: "demo"},
		Tag: "v9.9.9", WorkDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("an unknown tag was not rejected")
	}
}

const laterOutput = `{"config":{"scanner_version":"v1.1.4","db_last_modified":"2026-09-24T00:00:00Z"}}`

// secondRunFails audits a fresh release once (which always succeeds and
// records the first entry), then breaks the fake forge via breakIt, runs a
// second audit with secondOutput as govulncheck's result, and returns the
// client (for a test to inspect what actually got recorded) and the second
// run's error.
func secondRunFails(t *testing.T, secondOutput string, breakIt func(*controls)) (*github.Client, error) {
	t.Helper()
	fakeGovulncheck(t, cleanOutput)
	p := buildRelease(t)
	client, ctl := p.serve(t, "")
	opts := audit.Options{
		Client: client, Repo: github.Repo{Owner: "you", Name: "demo"},
		Tag: "v1.2.3",
	}

	opts.WorkDir = t.TempDir()
	if _, err := audit.Run(context.Background(), opts); err != nil {
		t.Fatalf("first audit.Run: %v", err)
	}

	fakeGovulncheck(t, secondOutput)
	ctl.mu.Lock()
	breakIt(ctl)
	ctl.mu.Unlock()

	opts.WorkDir = t.TempDir()
	_, err := audit.Run(context.Background(), opts)
	return client, err
}

// A release whose existing audit.json can't be downloaded fails the run
// rather than silently starting over (which would lose its history).
func TestRunFailsWhenTheExistingAuditJSONCannotBeDownloaded(t *testing.T) {
	_, err := secondRunFails(t, cleanOutput, func(ctl *controls) { ctl.failDownload = audit.FileName })
	if err == nil {
		t.Fatal("a failed audit.json download was not rejected")
	}
}

// A release whose existing audit.json does not parse fails the run rather
// than silently discarding its history.
func TestRunFailsWhenTheExistingAuditJSONDoesNotParse(t *testing.T) {
	_, err := secondRunFails(t, cleanOutput, func(ctl *controls) { ctl.corrupt = audit.FileName })
	if err == nil {
		t.Fatal("an unparseable audit.json was not rejected")
	}
}

// AU-8: a run that fails to replace the release's audit.json must not have
// removed the old one first and left the release with none at all.
func TestRunFailsWhenReplacingAuditJSONFails(t *testing.T) {
	client, err := secondRunFails(t, laterOutput, func(ctl *controls) { ctl.failDelete = audit.FileName })
	if err == nil {
		t.Fatal("a failed replace was not rejected")
	}

	record := fetchRecord(t, client)
	if len(record.Audits) != 1 {
		t.Fatalf("audits = %+v, want the original entry still intact", record.Audits)
	}
}

// A release audited for the first time whose audit.json upload fails must
// surface the error.
func TestRunFailsWhenTheFirstUploadFails(t *testing.T) {
	fakeGovulncheck(t, cleanOutput)
	p := buildRelease(t)
	client, ctl := p.serve(t, "")
	ctl.mu.Lock()
	ctl.failUpload = audit.FileName
	ctl.mu.Unlock()

	_, err := audit.Run(context.Background(), audit.Options{
		Client: client, Repo: github.Repo{Owner: "you", Name: "demo"},
		Tag: "v1.2.3", WorkDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("a failed audit.json upload was not rejected")
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// serveMany serves ListReleases across several releases at once, for
// RunAll: real ones (fully built, actually downloaded and scanned when
// RunAll keeps them) alongside bare metadata-only entries a filter should
// exclude before any asset is ever fetched (an older release in an
// already-represented major, a draft, a retracted release, a prerelease,
// or an out-of-scope tag).
func serveMany(t *testing.T, real []*published, bare []github.Release) *github.Client {
	t.Helper()

	byID := map[int64]served{}
	nextID := int64(100)
	releases := make([]github.Release, 0, len(real)+len(bare))
	for _, p := range real {
		var assets []github.Asset
		for _, name := range p.result.Files {
			data, err := os.ReadFile(filepath.Join(p.dist, name))
			if err != nil {
				t.Fatal(err)
			}
			nextID++
			byID[nextID] = served{name: name, data: data}
			assets = append(assets, github.Asset{ID: nextID, Name: name, Size: int64(len(data)), Digest: "sha256:" + sha256Hex(data)})
		}
		nextID++
		releases = append(releases, github.Release{ID: nextID, TagName: p.tag, Assets: assets})
	}
	releases = append(releases, bare...)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/releases/tags/"):
			tag := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			for _, rel := range releases {
				if rel.TagName == tag {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(rel)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)

		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/releases/") && strings.HasSuffix(r.URL.Path, "/assets"):
			name := r.URL.Query().Get("name")
			data, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			nextID++
			byID[nextID] = served{name: name, data: data}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(github.Asset{ID: nextID, Name: name, Size: int64(len(data)), Digest: "sha256:" + sha256Hex(data)})

		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/releases/assets/"):
			idText := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			assetID, _ := strconv.ParseInt(idText, 10, 64)
			delete(byID, assetID)
			w.WriteHeader(http.StatusNoContent)

		case strings.Contains(r.URL.Path, "/releases/assets/"):
			idText := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			assetID, _ := strconv.ParseInt(idText, 10, 64)
			s, ok := byID[assetID]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write(s.data)

		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/releases"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(releases)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	client := github.New("token")
	client.SetEndpoints(server.URL, server.URL)
	return client
}

// AU-1 / the PBS acceptance scenario: given releases spanning several
// majors, RunAll audits only the newest stable release of each. v0 forms
// its own major line rather than being excluded, per the HLD's decision.
func TestRunAllAuditsTheNewestReleaseOfEachMajor(t *testing.T) {
	fakeGovulncheck(t, cleanOutput)
	v0 := buildReleaseTagged(t, "v0.9.0")
	v1 := buildReleaseTagged(t, "v1.4.0")
	v2 := buildReleaseTagged(t, "v2.0.1")
	client := serveMany(t, []*published{v0, v1, v2}, []github.Release{
		{ID: 1, TagName: "v1.3.2"},
	})

	results, err := audit.RunAll(context.Background(), audit.Options{
		Client: client, Repo: github.Repo{Owner: "you", Name: "demo"}, WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("audit.RunAll: %v", err)
	}

	var got []string
	for _, r := range results {
		got = append(got, r.Tag)
	}
	want := []string{"v0.9.0", "v1.4.0", "v2.0.1"}
	if len(got) != len(want) {
		t.Fatalf("audited %v, want %v", got, want)
	}
	for i, tag := range want {
		if got[i] != tag {
			t.Errorf("audited %v, want %v", got, want)
			break
		}
	}
}

// A draft, a retracted release, and a prerelease must never reach
// auditRelease: none of them carries a built source archive here, so a
// filter failure would surface as a download/manifest error rather than a
// silent pass.
func TestRunAllExcludesDraftsRetractedAndPrereleases(t *testing.T) {
	client := serveMany(t, nil, []github.Release{
		{ID: 1, TagName: "v1.0.0", Draft: true},
		{ID: 2, TagName: "v2.0.0", Body: "> [!CAUTION]\nretracted"},
		{ID: 3, TagName: "v3.0.0-rc.1", Prerelease: true},
	})

	results, err := audit.RunAll(context.Background(), audit.Options{
		Client: client, Repo: github.Repo{Owner: "you", Name: "demo"}, WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("audit.RunAll: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("results = %+v, want none", results)
	}
}

// A tag outside the module's scope must not be audited as if it were this
// module's own release.
func TestRunAllExcludesOutOfScopeTags(t *testing.T) {
	client := serveMany(t, nil, []github.Release{
		{ID: 1, TagName: "v1.0.0"},
		{ID: 2, TagName: "web/v1.0.0"},
	})

	results, err := audit.RunAll(context.Background(), audit.Options{
		Client: client, Repo: github.Repo{Owner: "you", Name: "demo"},
		Prefix: "services/api/", WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("audit.RunAll: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("results = %+v, want none", results)
	}
}
