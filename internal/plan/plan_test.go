package plan_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/plan"
)

type repo struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	r := &repo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	return r
}

func (r *repo) git(args ...string) {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_AUTHOR_DATE=2024-03-15T12:30:45Z", "GIT_COMMITTER_DATE=2024-03-15T12:30:45Z",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func (r *repo) write(name, content string) {
	r.t.Helper()
	path := filepath.Join(r.dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *repo) commit(tag string) {
	r.t.Helper()
	r.git("add", ".")
	r.git("commit", "-q", "-m", "commit")
	if tag != "" {
		r.git("tag", tag)
	}
}

func (r *repo) resolve(opts plan.Options) *plan.Plan {
	r.t.Helper()
	opts.Dir = r.dir
	p, err := plan.Resolve(context.Background(), opts)
	if err != nil {
		r.t.Fatalf("Resolve: %v", err)
	}
	return p
}

func check(t *testing.T, p *plan.Plan, name string) plan.Check {
	t.Helper()
	for _, c := range p.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check named %q in %+v", name, p.Checks)
	return plan.Check{}
}

// A repository with no config file is the primary path.
func TestZeroConfigRelease(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nvar version = \"dev\"\n\nfunc main() {}\n")
	r.write("README.md", "# foo\n")
	r.commit("v1.2.3")

	p := r.resolve(plan.Options{})

	if !p.OK() {
		t.Errorf("plan should pass: %+v", p.Checks)
	}
	if p.Project != "foo" {
		t.Errorf("Project = %q, want foo", p.Project)
	}
	if p.Version != "1.2.3" {
		t.Errorf("Version = %q, want 1.2.3", p.Version)
	}
	if len(p.Artifacts) != 5 {
		t.Errorf("got %d artifacts, want 5", len(p.Artifacts))
	}
	if got := p.Artifacts[0].Name; !strings.HasPrefix(got, "foo_1.2.3_") {
		t.Errorf("artifact name = %q", got)
	}
	// Windows must get a zip; everything else a tarball.
	for _, a := range p.Artifacts {
		wantExt := ".tar.gz"
		if a.Target.OS == "windows" {
			wantExt = ".zip"
		}
		if !strings.HasSuffix(a.Name, wantExt) {
			t.Errorf("%s: want %s", a.Name, wantExt)
		}
	}
	if len(p.Files) == 0 || p.Files[0] != "README.md" {
		t.Errorf("Files = %v, want README.md included", p.Files)
	}
}

// The major-version gate has to fire through the plan, not only in isolation.
func TestMajorVersionGateFires(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.commit("v2.0.0")

	p := r.resolve(plan.Options{})

	if p.OK() {
		t.Fatal("plan passed with a v2 tag on an unsuffixed module path")
	}
	c := check(t, p, "module path")
	if c.Status != plan.Fail || !strings.Contains(c.Detail, "/v2") {
		t.Errorf("module path check = %+v", c)
	}
}

// A wrongly named version variable must fail before anything is built.
func TestVersionInjectionGateFires(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nconst version = \"dev\"\n\nfunc main() {}\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})

	c := check(t, p, "version injection")
	if c.Status != plan.Fail {
		t.Fatalf("version injection check = %+v, want a failure for a const", c)
	}
	if !strings.Contains(c.Detail, "constant") {
		t.Errorf("detail does not explain the problem: %q", c.Detail)
	}
}

// Not declaring the version variables at all is a legitimate choice.
func TestNoVersionVarsIsNotAFailure(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})

	if c := check(t, p, "version injection"); c.Status != plan.Skip {
		t.Errorf("version injection = %+v, want skip", c)
	}
	if !p.OK() {
		t.Errorf("plan should pass: %+v", p.Checks)
	}
}

func TestUntaggedHeadRequiresSnapshot(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.commit("")

	if p := r.resolve(plan.Options{}); p.OK() {
		t.Error("plan passed with no tag on HEAD")
	}

	snap := r.resolve(plan.Options{Snapshot: true})
	if !snap.OK() {
		t.Errorf("snapshot plan should pass: %+v", snap.Checks)
	}
	if !strings.Contains(snap.Version, "-next+") {
		t.Errorf("snapshot version = %q, want a -next+ suffix", snap.Version)
	}
}

func TestDirtyWorktreeBlocksRelease(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.commit("v1.0.0")
	r.write("main.go", "package main\n\n// edited\nfunc main() {}\n")

	if p := r.resolve(plan.Options{}); p.OK() {
		t.Error("plan passed with a dirty worktree")
	}
	// A warning, not a failure, when the result is explicitly not a release.
	p := r.resolve(plan.Options{AllowDirty: true})
	if c := check(t, p, "worktree"); c.Status != plan.Warn {
		t.Errorf("worktree check = %+v, want warn", c)
	}
}

func TestConfigOverridesDefaults(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.write("README.md", "# foo\n")
	r.write("NOTES.md", "notes\n")
	r.write("letsgo.mod", "project renamed\n\nbuild (\n\tlinux/arm64\n)\n\narchive NOTES.md\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})

	if !p.OK() {
		t.Fatalf("plan should pass: %+v", p.Checks)
	}
	if p.Project != "renamed" {
		t.Errorf("Project = %q, want renamed", p.Project)
	}
	if len(p.Targets) != 1 || p.Targets[0].Arch != "arm64" {
		t.Errorf("Targets = %v", p.Targets)
	}
	// An explicit archive list replaces the inferred one rather than adding
	// to it, or there would be no way to exclude a conventional file.
	if len(p.Files) != 1 || p.Files[0] != "NOTES.md" {
		t.Errorf("Files = %v, want only NOTES.md", p.Files)
	}
}

func TestBadConfigIsReportedNotIgnored(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.write("letsgo.mod", "buidl linux/amd64\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})
	if p.OK() {
		t.Fatal("plan passed with an unreadable config")
	}
	if c := check(t, p, "config"); !strings.Contains(c.Detail, "letsgo.mod:1") {
		t.Errorf("config error lacks a position: %q", c.Detail)
	}
}

// Several main packages mean the project name cannot identify an artifact.
func TestMultipleCommandsAreNamedIndividually(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.write("cmd/alpha/main.go", "package main\n\nfunc main() {}\n")
	r.write("cmd/beta/main.go", "package main\n\nfunc main() {}\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})

	if len(p.Commands) != 2 {
		t.Fatalf("found %d commands, want 2", len(p.Commands))
	}
	names := map[string]bool{}
	for _, a := range p.Artifacts {
		names[strings.SplitN(a.Name, "_", 2)[0]] = true
	}
	if !names["alpha"] || !names["beta"] {
		t.Errorf("artifact names = %v, want alpha and beta", names)
	}
}

func TestExplainNamesEverySource(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})

	var report strings.Builder
	p.Report(&report, true)

	for _, want := range []string{"go.mod", "git HEAD", "default matrix", "zero-config defaults"} {
		if !strings.Contains(report.String(), want) {
			t.Errorf("--explain output is missing %q:\n%s", want, report.String())
		}
	}
	for _, s := range p.Sources {
		if s.From == "" {
			t.Errorf("source %q has no origin", s.Field)
		}
	}
}
