package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/release"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

func TestDifferencesNamesEachFieldThatMoved(t *testing.T) {
	planned := `{"version":"1.3.0","artifacts":[{"sha256":"aaa"},{"sha256":"bbb"}],"builder":{"go":"1.27.1"}}`
	rebuilt := `{"version":"1.3.0","artifacts":[{"sha256":"aaa"},{"sha256":"ccc"}],"builder":{"go":"1.27.2"},"sbom":"x.json"}`

	got := strings.Join(differences([]byte(planned), []byte(rebuilt)), "\n")
	for _, want := range []string{
		"artifacts[1].sha256: bbb → ccc",
		"builder.go: 1.27.1 → 1.27.2",
		"sbom: absent → x.json",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("differences missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "version:") {
		t.Errorf("differences lists a field that did not move:\n%s", got)
	}
}

func TestDifferencesCapsALongList(t *testing.T) {
	a, b := map[string]int{}, map[string]int{}
	for i := range 40 {
		a[string(rune('a'+i%26))+strings.Repeat("x", i)] = 1
		b[string(rune('a'+i%26))+strings.Repeat("x", i)] = 2
	}
	pa, _ := json.Marshal(a)
	pb, _ := json.Marshal(b)

	got := differences(pa, pb)
	if len(got) != maxDifferences+1 || got[len(got)-1] != "…" {
		t.Errorf("got %d lines, last %q; want %d ending in an ellipsis", len(got), got[len(got)-1], maxDifferences+1)
	}
}

func TestDifferencesOfGarbageIsEmpty(t *testing.T) {
	if got := differences([]byte("nope"), []byte("{}")); got != nil {
		t.Errorf("differences = %v, want none", got)
	}
}

// rebuiltIn writes a manifest into a fresh directory, as a build would, and
// returns it with the digest a plan would record for it.
func rebuiltIn(t *testing.T, body string) (*release.Result, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, manifest.FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(body))
	return &release.Result{Dir: dir}, "sha256:" + hex.EncodeToString(sum[:])
}

func planFileFor(tag, commit, manifestBody, digest string) *plandiff.File {
	return &plandiff.File{
		Schema: plandiff.FileSchema, Kind: plandiff.FileKindRelease,
		Tag: tag, Commit: commit, ManifestSHA256: digest, Manifest: json.RawMessage(manifestBody),
	}
}

func resolvedPlan(tag, commit string) *plan.Plan {
	p := &plan.Plan{Tag: tag}
	p.Git.Commit = commit
	return p
}

func TestAgreedPlanAcceptsAnExactRebuild(t *testing.T) {
	body := `{"version":"1.3.0"}`
	result, digest := rebuiltIn(t, body)

	err := agreedPlan(planFileFor("v1.3.0", "abc", body, digest))(resolvedPlan("v1.3.0", "abc"), result)
	if err != nil {
		t.Errorf("agreedPlan = %v, want nil", err)
	}
}

func TestAgreedPlanRefusesADifferentTagOrCommit(t *testing.T) {
	body := `{"version":"1.3.0"}`
	result, digest := rebuiltIn(t, body)
	file := planFileFor("v1.3.0", "abcdef0123456789", body, digest)

	if err := agreedPlan(file)(resolvedPlan("v1.4.0", "abcdef0123456789"), result); err == nil ||
		!strings.Contains(err.Error(), "v1.3.0") || !strings.Contains(err.Error(), "v1.4.0") {
		t.Errorf("different tag: %v", err)
	}
	if err := agreedPlan(file)(resolvedPlan("v1.3.0", "fedcba9876543210"), result); err == nil ||
		!strings.Contains(err.Error(), "abcdef012345") {
		t.Errorf("different commit: %v", err)
	}
}

func TestAgreedPlanRefusesARebuildThatDiffersAndSaysWhere(t *testing.T) {
	planned := `{"version":"1.3.0","builder":{"go":"1.27.1"}}`
	_, plannedDigest := rebuiltIn(t, planned)
	result, _ := rebuiltIn(t, `{"version":"1.3.0","builder":{"go":"1.27.2"}}`)

	err := agreedPlan(planFileFor("v1.3.0", "abc", planned, plannedDigest))(resolvedPlan("v1.3.0", "abc"), result)
	if err == nil {
		t.Fatal("agreedPlan accepted a rebuild that differs")
	}
	for _, want := range []string{"does not match the plan", "nothing was published", "builder.go: 1.27.1 → 1.27.2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
}

func TestAgreedPlanReportsAMissingManifest(t *testing.T) {
	file := planFileFor("v1.3.0", "abc", "{}", "sha256:x")
	if err := agreedPlan(file)(resolvedPlan("v1.3.0", "abc"), &release.Result{Dir: t.TempDir()}); err == nil {
		t.Error("agreedPlan succeeded with no manifest to compare")
	}
}

func TestSavePlanWritesAReadableFile(t *testing.T) {
	p := resolvedPlan("v1.3.0", "abc")
	p.Repo.Owner, p.Repo.Name = "you", "demo"
	path := filepath.Join(t.TempDir(), "letsgo.plan")

	digest, err := savePlan(p, &forgeDiff{
		Actions:        []plandiff.Action{{Op: plandiff.Add, Kind: plandiff.KindAsset, Target: "a.zip", Planned: "sha256:aa"}},
		Manifest:       []byte(`{"version":"1.3.0"}`),
		ManifestSHA256: "sha256:bb",
	}, path)
	if err != nil {
		t.Fatal(err)
	}

	file, err := plandiff.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := file.Digest(); got != digest {
		t.Errorf("digest %s, printed %s", got, digest)
	}
	if file.Repo != "you/demo" || file.Tag != "v1.3.0" || file.Commit != "abc" || file.ManifestSHA256 != "sha256:bb" || len(file.Actions) != 1 {
		t.Errorf("saved %+v", file)
	}
}

func TestSavePlanReportsAnUnwritablePath(t *testing.T) {
	_, err := savePlan(resolvedPlan("v1", "abc"), &forgeDiff{Manifest: []byte(`{}`)}, filepath.Join(t.TempDir(), "no", "dir", "p"))
	if err == nil {
		t.Error("savePlan succeeded into a missing directory")
	}
}

func TestExitCodeTellsDriftFromFailure(t *testing.T) {
	if got := exitCode(errPlanChanges); got != 2 {
		t.Errorf("exitCode(drift) = %d, want 2", got)
	}
	if got := exitCode(errors.New("boom")); got != 1 {
		t.Errorf("exitCode(error) = %d, want 1", got)
	}
}

func TestRunApplyNeedsAPlanFile(t *testing.T) {
	if err := runApply(nil); err == nil || !strings.Contains(err.Error(), "plan file") {
		t.Errorf("runApply() = %v, want an error asking for a plan file", err)
	}
	if err := runApply([]string{filepath.Join(t.TempDir(), "absent")}); err == nil {
		t.Error("runApply accepted a missing file")
	}
}

func TestRunPlanRejectsExitCodeWithoutDiff(t *testing.T) {
	if err := runPlan([]string{"--exit-code"}); err == nil || !strings.Contains(err.Error(), "--diff") {
		t.Errorf("runPlan --exit-code = %v", err)
	}
	if err := runPlan([]string{"--json", "-out", "x"}); err == nil {
		t.Error("runPlan accepted --json with -out")
	}
}

func TestFinishDiffSavesThePlanAndAnswersExitCode(t *testing.T) {
	p := resolvedPlan("v1.3.0", "abc")
	p.Repo.Owner, p.Repo.Name = "you", "demo"
	changes := &forgeDiff{
		Actions:  []plandiff.Action{{Op: plandiff.Add, Kind: plandiff.KindAsset, Target: "a.zip", Planned: "sha256:aa"}},
		Manifest: []byte(`{}`), ManifestSHA256: "sha256:bb",
	}
	kept := &forgeDiff{
		Actions:  []plandiff.Action{{Op: plandiff.Keep, Kind: plandiff.KindAsset, Target: "a.zip", Observed: "sha256:aa", Planned: "sha256:aa"}},
		Manifest: []byte(`{}`), ManifestSHA256: "sha256:bb",
	}
	path := filepath.Join(t.TempDir(), "letsgo.plan")

	for name, tc := range map[string]struct {
		d       *forgeDiff
		run     diffRun
		wantErr error
		saved   bool
	}{
		"changes, plain":           {changes, diffRun{}, nil, false},
		"changes, exit code":       {changes, diffRun{ExitCode: true}, errPlanChanges, false},
		"no changes, exit code":    {kept, diffRun{ExitCode: true}, nil, false},
		"changes, saved":           {changes, diffRun{Out: path}, nil, true},
		"saved and exit code":      {changes, diffRun{Out: path, ExitCode: true}, errPlanChanges, true},
		"nothing to change, plain": {kept, diffRun{}, nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			_ = os.Remove(path)
			if err := finishDiff(p, tc.d, tc.run); !errors.Is(err, tc.wantErr) {
				t.Errorf("finishDiff = %v, want %v", err, tc.wantErr)
			}
			_, err := os.Stat(path)
			if saved := err == nil; saved != tc.saved {
				t.Errorf("plan file saved = %v, want %v", saved, tc.saved)
			}
		})
	}
}

func TestFinishDiffReportsAnUnwritablePlanPath(t *testing.T) {
	err := finishDiff(resolvedPlan("v1", "abc"), &forgeDiff{Manifest: []byte(`{}`)},
		diffRun{Out: filepath.Join(t.TempDir(), "no", "dir", "p")})
	if err == nil {
		t.Error("finishDiff succeeded writing into a missing directory")
	}
}

// emptyForge is a forge with nothing on it: every release lookup is a 404.
func emptyForge(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	t.Cleanup(srv.Close)

	previous := newForgeClient
	t.Cleanup(func() { newForgeClient = previous })
	newForgeClient = func(token string) *github.Client {
		client := github.New(token)
		client.SetEndpoints(srv.URL, srv.URL)
		return client
	}
}

func TestPlanOutSavesAPlanThatAnApplyWouldCheck(t *testing.T) {
	emptyForge(t)
	t.Chdir(moduleFixture(t))
	path := filepath.Join(t.TempDir(), "release.plan")

	var err error
	out := captureStdout(t, func() { err = runPlan([]string{"-out", path, "--exit-code"}) })
	if !errors.Is(err, errPlanChanges) {
		t.Fatalf("runPlan -out --exit-code = %v, want the plan to have changes\n%s", err, out)
	}
	for _, want := range []string{"+ release", "to add", "saved to " + path, "letsgo apply " + path} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	file, err := plandiff.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if file.Tag != "v1.2.3" || file.Repo != "you/demo" || !file.Changes() {
		t.Errorf("saved %+v", file)
	}

	// The saved manifest is what a rebuild is held to.
	if file.ManifestSHA256 == "" || len(file.Manifest) == 0 {
		t.Error("the plan carries no manifest digest")
	}
}

func TestPlanDiffWithoutARepositoryCannotCompare(t *testing.T) {
	_, err := planDiff(context.Background(), &plan.Plan{}, diffTokens{})
	if err == nil || !strings.Contains(err.Error(), "repository") {
		t.Errorf("planDiff = %v, want a complaint about the missing repository", err)
	}
}

func TestApplyStopsWhenThePlanCannotBeResolved(t *testing.T) {
	emptyForge(t)
	t.Chdir(moduleFixture(t))
	path := filepath.Join(t.TempDir(), "release.plan")
	_ = captureStdout(t, func() { _ = runPlan([]string{"-out", path}) })

	// With no token a release's forge checks fail, so apply stops before it
	// builds: the point is that it neither panics nor publishes.
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	var err error
	out := captureStdout(t, func() { err = runApply([]string{path}) })
	if err == nil {
		t.Errorf("runApply succeeded without a forge token:\n%s", out)
	}
	if !strings.Contains(out, "applying "+path) {
		t.Errorf("output does not say what is being applied:\n%s", out)
	}
}

func TestPlanFormatMarkdownWritesOnlyTheSummaryToStdout(t *testing.T) {
	emptyForge(t)
	t.Chdir(moduleFixture(t))

	var err error
	out := captureStdout(t, func() { err = runPlan([]string{"--format", "md"}) })
	if err != nil {
		t.Fatalf("runPlan --format md = %v\n%s", err, out)
	}
	if !strings.HasPrefix(out, "## letsgo plan\n\n```diff\n+ release") {
		t.Errorf("summary does not open with a heading and a diff block:\n%s", out)
	}
	if !strings.Contains(out, "```\n\nPlan: ") || !strings.HasSuffix(out, "to remove.\n") {
		t.Errorf("summary does not close the block and end on the footer:\n%s", out)
	}
	for _, leaked := range []string{"plan ok", "resolved", "  + release"} {
		if strings.Contains(out, leaked) {
			t.Errorf("stdout carries %q, which belongs on stderr:\n%s", leaked, out)
		}
	}
	if os.Stdout == os.Stderr {
		t.Error("standard output was left pointing at standard error")
	}
}

func TestPlanFormatRejectsWhatItCannotWrite(t *testing.T) {
	if err := runPlan([]string{"--format", "yaml"}); err == nil || !strings.Contains(err.Error(), "yaml") {
		t.Errorf("--format yaml = %v", err)
	}
	if err := runPlan([]string{"--format", "md", "--json"}); err == nil || !strings.Contains(err.Error(), "--json") {
		t.Errorf("--format md --json = %v", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestFinishPlanReportsASummaryItCannotWrite(t *testing.T) {
	err := finishPlan(nil, nil, diffRun{Markdown: failingWriter{}, Title: "letsgo plan"})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("finishPlan = %v, want the write failure", err)
	}
}
