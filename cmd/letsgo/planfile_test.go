package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/apply"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

func TestExitCodeTellsDriftFromFailure(t *testing.T) {
	if got := exitCode(errPlanChanges); got != 2 {
		t.Errorf("exitCode(drift) = %d, want 2", got)
	}
	if got := exitCode(errors.New("boom")); got != 1 {
		t.Errorf("exitCode(error) = %d, want 1", got)
	}
}

func TestRunApplyRejectsWhatItCannotApply(t *testing.T) {
	if err := runApply([]string{filepath.Join(t.TempDir(), "absent")}); err == nil {
		t.Error("runApply accepted a missing file")
	}
	if err := runApply([]string{"a.plan", "b.plan"}); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("runApply with two files = %v, want usage", err)
	}
}

// answeringStdin makes standard input a terminal that answers with reply.
func answeringStdin(t *testing.T, reply string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(reply); err != nil {
		t.Fatal(err)
	}
	w.Close()

	stdin, terminal := os.Stdin, stdinIsTerminal
	t.Cleanup(func() { os.Stdin, stdinIsTerminal = stdin, terminal; r.Close() })
	os.Stdin, stdinIsTerminal = r, func() bool { return true }
}

func TestApplyWithNoFileNeedsATerminalOrAutoApprove(t *testing.T) {
	terminal := stdinIsTerminal
	t.Cleanup(func() { stdinIsTerminal = terminal })
	stdinIsTerminal = func() bool { return false }

	// Nothing is planned, let alone built: the question is settled first.
	t.Chdir(t.TempDir())
	out := captureStdout(t, func() {
		err := runApply(nil)
		if err == nil || !strings.Contains(err.Error(), "-auto-approve") {
			t.Errorf("runApply() = %v, want a request for -auto-approve", err)
		}
	})
	if out != "" {
		t.Errorf("apply printed before refusing:\n%s", out)
	}
}

func TestApplyWithNoFileShowsThePlanAndStopsOnNo(t *testing.T) {
	writableForge(t)
	t.Chdir(moduleFixture(t))
	answeringStdin(t, "n\n")

	var err error
	out := captureStdout(t, func() { err = runApply(nil) })
	if err != nil {
		t.Fatalf("runApply() = %v\n%s", err, out)
	}
	for _, want := range []string{"+ release", "to add", "Apply? [y/N]", "nothing was published"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "applying ") {
		t.Errorf("a declined apply went on to apply:\n%s", out)
	}
}

func TestApplyWithNoFileAppliesThePlanItShowedOnYes(t *testing.T) {
	for name, tc := range map[string]struct {
		args  []string
		reply string
	}{
		"yes at the prompt": {nil, "y\n"},
		"auto-approve":      {[]string{"-auto-approve"}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			writableForge(t)
			t.Chdir(moduleFixture(t))
			answeringStdin(t, tc.reply)

			out := captureStdout(t, func() { _ = runApply(tc.args) })
			if !strings.Contains(out, "applying ") || !strings.Contains(out, "for v1.2.3") {
				t.Errorf("the shown plan was not applied:\n%s", out)
			}
			if strings.Contains(out, "saved to") {
				t.Errorf("apply talks about a plan file the user never asked for:\n%s", out)
			}
		})
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
	changes := &apply.Diff{
		Actions:  []plandiff.Action{{Op: plandiff.Add, Kind: plandiff.KindAsset, Target: "a.zip", Planned: "sha256:aa"}},
		Manifest: []byte(`{}`), ManifestSHA256: "sha256:bb",
	}
	kept := &apply.Diff{
		Actions:  []plandiff.Action{{Op: plandiff.Keep, Kind: plandiff.KindAsset, Target: "a.zip", Observed: "sha256:aa", Planned: "sha256:aa"}},
		Manifest: []byte(`{}`), ManifestSHA256: "sha256:bb",
	}
	path := filepath.Join(t.TempDir(), "letsgo.plan")

	for name, tc := range map[string]struct {
		d       *apply.Diff
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
	err := finishDiff(resolvedPlan("v1", "abc"), &apply.Diff{Manifest: []byte(`{}`)},
		diffRun{Out: filepath.Join(t.TempDir(), "no", "dir", "p")})
	if err == nil {
		t.Error("finishDiff succeeded writing into a missing directory")
	}
}

// emptyForge is a forge with nothing on it: every release lookup is a 404.
func emptyForge(t *testing.T) {
	t.Helper()
	serveForge(t, false)
}

// writableForge is emptyForge that also says the token may write to the
// repository, which is what a plan that publishes checks first.
func writableForge(t *testing.T) {
	t.Helper()
	endpoint := serveForge(t, true)
	previous := forgeAPIEndpoint
	t.Cleanup(func() { forgeAPIEndpoint = previous })
	forgeAPIEndpoint = endpoint
	t.Setenv("GITHUB_TOKEN", "test-token")
}

func serveForge(t *testing.T, writable bool) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if writable && r.Method == http.MethodGet && r.URL.Path == "/repos/you/demo" {
			_, _ = w.Write([]byte(`{"permissions":{"push":true}}`))
			return
		}
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
	return srv.URL
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

func TestApplyWithNoFileStopsWhenThePlanFails(t *testing.T) {
	emptyForge(t)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Chdir(moduleFixture(t))

	var err error
	out := captureStdout(t, func() { err = runApply([]string{"-auto-approve"}) })
	if !errors.Is(err, errPlanFailed) {
		t.Errorf("runApply -auto-approve = %v, want the failed plan\n%s", err, out)
	}
	if strings.Contains(out, "applying ") {
		t.Errorf("a failed plan was applied:\n%s", out)
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

func resolvedPlan(tag, commit string) *plan.Plan {
	p := &plan.Plan{Tag: tag}
	p.Git.Commit = commit
	return p
}

// A fresh apply plans and builds as one: --no-proxy-warm must be in the plan
// it saves, or the rebuild would never match it.
func TestApplyWithNoProxyWarmPlansWithoutIt(t *testing.T) {
	writableForge(t)
	t.Chdir(moduleFixture(t))
	path := filepath.Join(t.TempDir(), "release.plan")

	var err error
	_ = captureStdout(t, func() { _, err = planForApply(t.Context(), releaseArgs{skipWarm: true}, path) })
	if err != nil {
		t.Fatal(err)
	}
	file, err := plandiff.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(file.Manifest), "proxy-warm") {
		t.Errorf("the plan's manifest does not record proxy-warm as disabled:\n%s", file.Manifest)
	}
}
