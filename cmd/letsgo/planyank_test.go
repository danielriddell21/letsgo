package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/danielriddell21/letsgo/internal/apply"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/yank"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

// yankForge is a forge holding one release, which it lets be edited.
type yankForge struct {
	mu      sync.Mutex
	release github.Release
	patches int
}

// yankForgeFor serves the release v1.2.3 of you/demo and points the commands
// at it.
func yankForgeFor(t *testing.T) *yankForge {
	t.Helper()
	forge := &yankForge{release: github.Release{ID: 7, TagName: "v1.2.3", Body: "### Features\n\n- a thing\n"}}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forge.mu.Lock()
		defer forge.mu.Unlock()

		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/releases/tags/v1.2.3"):
			_ = json.NewEncoder(w).Encode(forge.release)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/tags"):
			_, _ = w.Write([]byte(`[{"name":"v1.2.3"},{"name":"v1.2.2"}]`))
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/releases/7"):
			forge.patches++
			var in github.ReleaseInput
			_ = json.NewDecoder(r.Body).Decode(&in)
			forge.release.Body, forge.release.Prerelease = in.Body, in.Prerelease
			_ = json.NewEncoder(w).Encode(forge.release)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		}
	}))
	t.Cleanup(srv.Close)

	previous := newForgeClient
	t.Cleanup(func() { newForgeClient = previous })
	newForgeClient = func(token string) *github.Client {
		client := github.New(token)
		client.SetEndpoints(srv.URL, srv.URL)
		return client
	}
	t.Setenv("GITHUB_TOKEN", "test-token")
	return forge
}

// A yank is planned, saved, and applied, and the apply does what the plan
// listed and nothing else.
func TestAYankPlanIsAppliedExactlyAsPlanned(t *testing.T) {
	forge := yankForgeFor(t)
	dir := moduleFixture(t)
	t.Chdir(dir)
	path := filepath.Join(t.TempDir(), "y.plan")

	var err error
	out := captureStdout(t, func() {
		err = runPlan([]string{"-yank", "v1.2.3", "-reason", "bad build", "-out", path, "--exit-code"})
	})
	if !errors.Is(err, errPlanChanges) {
		t.Fatalf("plan -yank = %v, want the plan to have changes\n%s", err, out)
	}
	for _, want := range []string{"~ release", "~ gomod", "v1.2.3", "go.mod", "saved to " + path} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if forge.patches != 0 {
		t.Fatalf("planning edited the release %d time(s)", forge.patches)
	}
	if got := readText(t, filepath.Join(dir, "go.mod")); strings.Contains(got, "retract") {
		t.Fatalf("planning edited go.mod:\n%s", got)
	}

	file, err := plandiff.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if file.Kind != plandiff.FileKindYank || file.Tag != "v1.2.3" || file.Reason != "bad build" || file.Previous != "v1.2.2" {
		t.Errorf("saved %+v", file)
	}

	out = captureStdout(t, func() { err = runApply([]string{path}) })
	if err != nil {
		t.Fatalf("apply = %v\n%s", err, out)
	}
	if forge.patches != 1 || !forge.release.Prerelease || !yank.IsRetracted(forge.release.Body) {
		t.Errorf("patches = %d, release = %+v", forge.patches, forge.release)
	}
	if got := readText(t, filepath.Join(dir, "go.mod")); !strings.Contains(got, "v1.2.3 // bad build") {
		t.Errorf("go.mod:\n%s", got)
	}
	if !strings.Contains(out, "v1.2.3 is retracted") {
		t.Errorf("apply did not say what comes next:\n%s", out)
	}

	// Applying it again finds everything as planned, and writes nothing.
	out = captureStdout(t, func() { err = runApply([]string{path}) })
	if err != nil {
		t.Fatalf("second apply = %v\n%s", err, out)
	}
	if forge.patches != 1 || !strings.Contains(out, "already as planned") {
		t.Errorf("second apply: patches = %d\n%s", forge.patches, out)
	}
}

// A go.mod that changed after the plan was made is not the go.mod the plan
// agreed, so nothing is written.
func TestAYankPlanIsRefusedOnceGoModHasChanged(t *testing.T) {
	forge := yankForgeFor(t)
	dir := moduleFixture(t)
	t.Chdir(dir)
	path := filepath.Join(t.TempDir(), "y.plan")

	_ = captureStdout(t, func() { _ = runPlan([]string{"-yank", "v1.2.3", "-out", path}) })
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/demo\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var err error
	_ = captureStdout(t, func() { err = runApply([]string{path}) })
	if err == nil || !strings.Contains(err.Error(), "stale") || !strings.Contains(err.Error(), "plan -yank v1.2.3 -out") {
		t.Fatalf("apply = %v, want a stale plan", err)
	}
	if forge.patches != 0 {
		t.Errorf("a stale plan edited the release")
	}
}

func TestAYankPlanIsRefusedForAnotherRepository(t *testing.T) {
	yankForgeFor(t)
	t.Chdir(moduleFixture(t))
	path := filepath.Join(t.TempDir(), "y.plan")
	_ = captureStdout(t, func() { _ = runPlan([]string{"-yank", "v1.2.3", "-out", path}) })

	file, err := plandiff.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	file.Repo = "someone/else"
	if _, err := apply.Write(file, path); err != nil {
		t.Fatal(err)
	}

	_ = captureStdout(t, func() { err = runApply([]string{path}) })
	if err == nil || !strings.Contains(err.Error(), "someone/else") {
		t.Errorf("apply = %v, want the repository named", err)
	}
}

func TestPlanYankHasNoJSONForm(t *testing.T) {
	if err := runPlan([]string{"-yank", "v1.2.3", "-json"}); err == nil || !strings.Contains(err.Error(), "JSON") {
		t.Errorf("err = %v", err)
	}
}

func TestPlanYankNamesAReleaseThatDoesNotExist(t *testing.T) {
	yankForgeFor(t)
	t.Chdir(moduleFixture(t))
	var err error
	_ = captureStdout(t, func() { err = runPlan([]string{"-yank", "v9.9.9"}) })
	if err == nil {
		t.Error("planned the retraction of a release that does not exist")
	}
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPlanYankFormatMarkdown(t *testing.T) {
	yankForgeFor(t)
	t.Chdir(moduleFixture(t))

	var err error
	out := captureStdout(t, func() { err = runPlan([]string{"-yank", "v1.2.3", "--format", "md"}) })
	if err != nil {
		t.Fatalf("plan -yank --format md = %v\n%s", err, out)
	}
	for _, want := range []string{"## letsgo plan: yank v1.2.3\n", "```diff\n! release", "\n! gomod"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
}
