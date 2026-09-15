package plan_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/bytesize"
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

func TestBudgetsAreParsedAtPlanTime(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.write("letsgo.mod", "build linux/amd64\n\nbudget linux/amd64 15MB\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})

	if !p.OK() {
		t.Fatalf("plan should pass: %+v", p.Checks)
	}
	if got := p.Budgets["linux/amd64"]; got != 15*bytesize.MB {
		t.Errorf("budget = %v, want 15MB", got)
	}
}

// A malformed size would otherwise surface after a full matrix had been
// compiled, which is the one moment it is least useful.
func TestBadBudgetSizeFailsThePlan(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.write("letsgo.mod", "build linux/amd64\n\nbudget linux/amd64 fifteen\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})

	if p.OK() {
		t.Fatal("plan passed with an unparseable budget")
	}
	if c := check(t, p, "budgets"); !strings.Contains(c.Detail, "fifteen") {
		t.Errorf("budget error does not name the value: %q", c.Detail)
	}
}

// A budget on a target nobody builds never fires, which defeats the point of
// writing one down.
func TestBudgetForAnUnbuiltTargetFailsThePlan(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.write("letsgo.mod", "build linux/amd64\n\nbudget linux/arm64 15MB\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})

	if p.OK() {
		t.Fatal("plan passed with a budget for a target it does not build")
	}
	if c := check(t, p, "budgets"); !strings.Contains(c.Detail, "linux/arm64") {
		t.Errorf("budget error does not name the target: %q", c.Detail)
	}
}

func TestImageDefaultsToTheRepositoryOwner(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.write("letsgo.mod", "build linux/amd64\n\nimage\n")
	r.commit("v1.0.0")
	r.git("remote", "add", "origin", "https://github.com/you/foo.git")

	p := r.resolve(plan.Options{})

	if !p.OK() {
		t.Fatalf("plan should pass: %+v", p.Checks)
	}
	if p.Image == nil {
		t.Fatal("Image = nil")
	}
	if p.Image.Registry != "ghcr.io" || p.Image.Repository != "you/foo" {
		t.Errorf("image = %s/%s", p.Image.Registry, p.Image.Repository)
	}
	if len(p.Image.Platforms) != 1 {
		t.Errorf("platforms = %v", p.Image.Platforms)
	}
}

// The tag comes from the release. Accepting one here would create two answers
// to what a release is called and let them disagree.
func TestImageReferenceMayNotCarryATag(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.write("letsgo.mod", "build linux/amd64\n\nimage ghcr.io/you/foo:v1\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})

	if p.OK() {
		t.Fatal("plan passed with a tagged image reference")
	}
	if c := check(t, p, "image"); !strings.Contains(c.Detail, "tag") {
		t.Errorf("image error = %q", c.Detail)
	}
}

// An image with no Linux target would be a request nothing could satisfy.
func TestImageNeedsALinuxTarget(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.write("letsgo.mod", "build darwin/arm64\n\nimage ghcr.io/you/foo\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})

	if p.OK() {
		t.Fatal("plan passed with no linux target")
	}
	if c := check(t, p, "image"); !strings.Contains(c.Detail, "linux") {
		t.Errorf("image error = %q", c.Detail)
	}
}

// A base named by tag can change under a release, so it is reported — but it
// is a real thing to want, so it does not block.
func TestUnpinnedBaseWarnsWithoutBlocking(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.write("letsgo.mod", "build linux/amd64\n\nimage ghcr.io/you/foo\nimage base gcr.io/distroless/static:nonroot\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})

	if !p.OK() {
		t.Fatalf("an unpinned base should not block: %+v", p.Checks)
	}
	c := check(t, p, "image")
	if c.Status != plan.Warn || !strings.Contains(c.Detail, "sha256") {
		t.Errorf("image check = %+v", c)
	}
}

// No directive means no image, and no check either: a repository that did not
// ask should see nothing about registries at all.
func TestNoImageConfiguredMeansNoImageCheck(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})

	if p.Image != nil {
		t.Errorf("Image = %+v, want nil", p.Image)
	}
	for _, c := range p.Checks {
		if c.Name == "image" {
			t.Errorf("an image check appeared unasked: %+v", c)
		}
	}
}

// `archive scripts` must expand to the tracked files beneath it: the set is
// fixed by the commit, so it pins as tightly as a literal list while staying
// correct when a file is added to the directory.
func TestArchiveDirectoryExpandsToTrackedFiles(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.write("README.md", "# foo\n")
	r.write("config.example.yaml", "example: true\n")
	r.write("scripts/nzbget/foo.py", "print('a')\n")
	r.write("scripts/sabnzbd/foo.py", "print('b')\n")
	r.write("letsgo.mod", "archive (\n\tREADME.md\n\tconfig.example.yaml\n\tscripts\n)\n")
	r.commit("v1.0.0")

	// Untracked files in the directory are not shipped: a release contains
	// what a clone at this commit contains.
	r.write("scripts/scratch.tmp", "local\n")

	p := r.resolve(plan.Options{})

	want := []string{
		"README.md",
		"config.example.yaml",
		"scripts/nzbget/foo.py",
		"scripts/sabnzbd/foo.py",
	}
	if strings.Join(p.Files, "\n") != strings.Join(want, "\n") {
		t.Errorf("Files = %q, want %q", p.Files, want)
	}
}

// A glob resolves against whatever is on disk at release time, which the
// config does not pin. Saying so at plan time catches the GoReleaser habit in
// two seconds rather than after the cross-compile.
func TestArchiveRejectsGlobsAtPlanTime(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.write("scripts/foo.py", "print('a')\n")
	r.write("letsgo.mod", "archive scripts/**/*\n")
	r.commit("v1.0.0")

	p := r.resolve(plan.Options{})

	c := check(t, p, "archive files")
	if c.Status != plan.Fail {
		t.Fatalf("status = %s, want fail: %+v", c.Status, c)
	}
	if !strings.Contains(c.Detail, "paths, not patterns") {
		t.Errorf("detail = %q", c.Detail)
	}
}

// workspace builds the two-module layout the module directive exists for: a
// renderer split into its own module so that importing the core does not pull
// in its dependencies.
func workspace(t *testing.T, config string) *repo {
	t.Helper()
	r := newRepo(t)
	r.write("go.work", "go 1.24\n\nuse (\n\t.\n\t./web\n)\n")
	r.write("go.mod", "module github.com/you/merkelbrot\n\ngo 1.24\n")
	r.write("core.go", "package merkelbrot\n\nfunc Render() string { return \"core\" }\n")
	r.write("README.md", "# merkelbrot\n")
	r.write("LICENCE", "MIT\n")
	r.write("web/go.mod", "module github.com/you/merkelbrot/web\n\ngo 1.24\n")
	r.write("web/internal/buildinfo/buildinfo.go", "package buildinfo\n\nvar Version = \"dev\"\n")
	r.write("web/cmd/merkelbrot/main.go", "package main\n\nfunc main() {}\n")
	r.write("letsgo.mod", config)
	r.commit("v1.2.3")
	return r
}

func TestModuleDirectiveBuildsANestedModule(t *testing.T) {
	r := workspace(t, "module web\nbuild linux/amd64\n")
	p := r.resolve(plan.Options{})

	if !p.OK() {
		t.Fatalf("plan should pass: %+v", p.Checks)
	}
	if p.Module.Path != "github.com/you/merkelbrot/web" {
		t.Errorf("Module.Path = %q", p.Module.Path)
	}
	if p.RootDir != r.dir {
		t.Errorf("RootDir = %q, want the repository root %q", p.RootDir, r.dir)
	}
	// Releasing ./web does not rename the project after a directory.
	if p.Project != "merkelbrot" {
		t.Errorf("Project = %q, want merkelbrot", p.Project)
	}
	// Documentation lives in the repository, not in the module.
	if strings.Join(p.Files, ",") != "README.md,LICENCE" {
		t.Errorf("Files = %q, want the repository root's", p.Files)
	}
	if len(p.Commands) != 1 || p.Commands[0].RelPath != "./cmd/merkelbrot" {
		t.Errorf("Commands = %+v", p.Commands)
	}
}

// Pointing at a directory with no go.mod would otherwise resolve to an
// ancestor's, which is a silent no-op rather than the module that was asked for.
func TestModuleDirectiveRequiresAGoMod(t *testing.T) {
	r := workspace(t, "module internal\n")
	r.write("internal/thing.go", "package internal\n")
	r.commit("")

	c := check(t, r.resolve(plan.Options{}), "module")
	if c.Status != plan.Fail || !strings.Contains(c.Detail, "no go.mod") {
		t.Errorf("check = %+v", c)
	}
}

func TestVersionDirectiveInjectsIntoANamedSymbol(t *testing.T) {
	r := workspace(t, "module web\nbuild linux/amd64\nversion internal/buildinfo.Version\n")
	p := r.resolve(plan.Options{})

	if !p.OK() {
		t.Fatalf("plan should pass: %+v", p.Checks)
	}
	want := "github.com/you/merkelbrot/web/internal/buildinfo.Version"
	if p.Symbols.Version != want {
		t.Errorf("Symbols.Version = %q, want %q", p.Symbols.Version, want)
	}
	// Only the version was named, so the rest keep the conventional targets.
	if p.Symbols.Commit != "main.commit" || p.Symbols.Date != "main.date" {
		t.Errorf("Symbols = %+v", p.Symbols)
	}
}

// Asking for injection into a variable that does not exist is a mistake, and
// shipping binaries that report their compiled-in default is how it shows up.
func TestVersionDirectiveFailsOnAnUndeclaredSymbol(t *testing.T) {
	for _, config := range []string{
		"module web\nversion internal/buildinfo.Missing\n",
		"module web\nversion internal/nosuchpackage.Version\n",
	} {
		c := check(t, workspace(t, config).resolve(plan.Options{}), "version injection")
		if c.Status != plan.Fail {
			t.Errorf("%q: status = %s, want fail (%s)", config, c.Status, c.Detail)
		}
	}
}

func TestTagsAreResolvedAndValidated(t *testing.T) {
	r := workspace(t, "module web\nbuild linux/amd64\ntags netgo osusergo\n")
	if got := r.resolve(plan.Options{}).Tags; strings.Join(got, ",") != "netgo,osusergo" {
		t.Errorf("Tags = %q", got)
	}

	// -tags takes a comma-separated list, so a comma inside one would silently
	// become two tags.
	bad := workspace(t, "module web\ntags with,comma\n")
	if c := check(t, bad.resolve(plan.Options{}), "tags"); c.Status != plan.Fail {
		t.Errorf("check = %+v", c)
	}
}
