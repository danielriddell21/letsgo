package release_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/release"
	"github.com/danielriddell21/letsgo/manifest"
)

// A module that reports its injected version, so the smoke check has
// something real to verify.
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
		return
	}
}
`

// writeFile writes a fixture file, creating its parent directory as needed.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// gitFixture commits everything written into dir and tags it, with a fixed
// identity and timestamp so the fixture is reproducible.
func gitFixture(t *testing.T, dir, remote, tag string) {
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
	run("remote", "add", "origin", remote)
	run("add", ".")
	run("commit", "-q", "-m", "feat: first release")
	run("tag", tag)
}

// fixture builds a plan for a minimal releasable module. Extra letsgo.mod
// lines — a `disable`, say — can be given beyond the target it always needs.
func fixture(t *testing.T, extraConfig ...string) *plan.Plan {
	t.Helper()
	dir := t.TempDir()

	writeFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.24\n")
	writeFile(t, dir, "main.go", mainGo)
	writeFile(t, dir, "README.md", "# demo\n")
	config := "build " + gobuild.Host().String() + "\n"
	for _, line := range extraConfig {
		config += line + "\n"
	}
	writeFile(t, dir, "letsgo.mod", config)

	gitFixture(t, dir, "https://github.com/you/demo.git", "v1.2.3")

	p, err := plan.Resolve(context.Background(), plan.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !p.OK() {
		t.Fatalf("plan did not pass: %+v", p.Checks)
	}
	return p
}

// scopedFixture builds a plan for a module nested in a monorepo, tagged with
// its own prefixed tag, the same layout internal/plan's TestScopedRelease
// uses.
func scopedFixture(t *testing.T) *plan.Plan {
	t.Helper()
	dir := t.TempDir()

	writeFile(t, dir, "go.mod", "module example.com/monorepo\n\ngo 1.24\n")
	writeFile(t, dir, "main.go", mainGo)
	writeFile(t, dir, "services/api/go.mod", "module example.com/monorepo/services/api\n\ngo 1.24\n")
	writeFile(t, dir, "services/api/main.go", mainGo)
	writeFile(t, dir, "services/api/README.md", "# api\n")
	writeFile(t, dir, "services/api/letsgo.mod", "build "+gobuild.Host().String()+"\n")

	gitFixture(t, dir, "https://github.com/you/monorepo.git", "services/api/v1.2.0")

	p, err := plan.Resolve(context.Background(), plan.Options{Dir: filepath.Join(dir, "services/api")})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !p.OK() {
		t.Fatalf("plan did not pass: %+v", p.Checks)
	}
	return p
}

// A scoped release's manifest has to carry the prefix separately from Tag,
// so a reader can recover the plain version without assuming Tag's shape.
func TestBuildRecordsTheTagPrefixForAScopedRelease(t *testing.T) {
	p := scopedFixture(t)

	result, err := release.Build(context.Background(), p, t.TempDir(), "0.1.0", nil, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	m := result.Manifest
	if m.Tag != "services/api/v1.2.0" || m.Version != "1.2.0" {
		t.Fatalf("manifest header = %+v", m)
	}
	if m.TagPrefix != "services/api/" {
		t.Errorf("TagPrefix = %q, want %q", m.TagPrefix, "services/api/")
	}
}

func TestBuildProducesACompleteRelease(t *testing.T) {
	p := fixture(t)
	dir := t.TempDir()

	result, err := release.Build(context.Background(), p, dir, "0.1.0", nil, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Every file the manifest promises must exist on disk.
	for _, name := range result.Files {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s is listed but missing: %v", name, err)
		}
	}
	for _, want := range []string{manifest.FileName, build.ChecksumFile} {
		if !contains(result.Files, want) {
			t.Errorf("%s is not among the published files: %v", want, result.Files)
		}
	}
	if !strings.HasSuffix(result.Source.Name, "_source.tar.gz") {
		t.Errorf("source archive named %q", result.Source.Name)
	}

	m := result.Manifest
	if m.Schema != manifest.Schema || m.Version != "1.2.3" || m.Tag != "v1.2.3" {
		t.Errorf("manifest header = %+v", m)
	}
	if m.TagPrefix != "" {
		t.Errorf("TagPrefix = %q, want empty for a root module", m.TagPrefix)
	}
	if m.SourceDateEpoch != p.Git.CommitTime.Unix() {
		t.Errorf("SourceDateEpoch = %d, want the commit time", m.SourceDateEpoch)
	}
	if !strings.HasPrefix(m.Builder.Go, "go1.") {
		t.Errorf("Builder.Go = %q", m.Builder.Go)
	}
	if m.Builder.Tool != "letsgo 0.1.0" {
		t.Errorf("Builder.Tool = %q", m.Builder.Tool)
	}
	if m.Gates["version injection"] != "pass" {
		t.Errorf("gates were not recorded: %+v", m.Gates)
	}
}

// disable sbom must reach every layer: no asset on disk, nothing in
// SHA256SUMS, and the manifest saying why an older release's SBOM would
// still be there and this one's is not.
func TestDisableSBOMOmitsItEverywhere(t *testing.T) {
	p := fixture(t, "disable sbom")
	dir := t.TempDir()

	result, err := release.Build(context.Background(), p, dir, "0.1.0", nil, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if result.Manifest.SBOM != "" {
		t.Errorf("manifest SBOM = %q, want empty", result.Manifest.SBOM)
	}
	if contains(result.Files, "sbom.cdx.json") || result.Manifest.Features == nil ||
		!contains(result.Manifest.Features.Disabled, "sbom") {
		t.Errorf("Files = %v, Features = %+v", result.Files, result.Manifest.Features)
	}

	sums, err := os.ReadFile(filepath.Join(dir, build.ChecksumFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sums), "sbom") {
		t.Errorf("SHA256SUMS mentions the SBOM it did not write:\n%s", sums)
	}
}

// The manifest's digests are what verification and resumable upload both rely
// on. If they do not describe the bytes on disk, everything downstream is
// quietly wrong.
func TestManifestDigestsMatchTheFilesOnDisk(t *testing.T) {
	p := fixture(t)
	dir := t.TempDir()

	result, err := release.Build(context.Background(), p, dir, "0.1.0", nil, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	for _, a := range result.Manifest.Artifacts {
		data, err := os.ReadFile(filepath.Join(dir, a.Name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != a.SHA256 {
			t.Errorf("%s: manifest says %s, file is %s", a.Name, a.SHA256, got)
		}
		if int64(len(data)) != a.Size {
			t.Errorf("%s: manifest says %d bytes, file is %d", a.Name, a.Size, len(data))
		}
		if a.Build.LDFlags == "" || !strings.Contains(a.Build.LDFlags, "main.version=1.2.3") {
			t.Errorf("%s: ldflags not recorded: %q", a.Name, a.Build.LDFlags)
		}
	}

	data, err := os.ReadFile(filepath.Join(dir, result.Source.Name))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != result.Manifest.Source.SHA256 {
		t.Errorf("source archive digest mismatch: manifest %s, file %s",
			result.Manifest.Source.SHA256, got)
	}
}

// SHA256SUMS has to cover the manifest too, so that trusting one file is
// enough to trust every digest the release publishes.
func TestChecksumFileCoversEverythingElse(t *testing.T) {
	p := fixture(t)
	dir := t.TempDir()

	result, err := release.Build(context.Background(), p, dir, "0.1.0", nil, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, build.ChecksumFile))
	if err != nil {
		t.Fatal(err)
	}

	listed := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			listed[fields[1]] = fields[0]
		}
	}

	for _, name := range result.Files {
		if name == build.ChecksumFile {
			continue // a checksum file cannot contain its own digest
		}
		digest, ok := listed[name]
		if !ok {
			t.Errorf("%s is not listed in %s", name, build.ChecksumFile)
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != digest {
			t.Errorf("%s: checksum file disagrees with the file", name)
		}
	}
}

// Building the same plan twice must produce identical artifacts, or none of
// verification, resume, or diffing means anything.
func TestBuildIsReproducible(t *testing.T) {
	p := fixture(t)

	first, err := release.Build(context.Background(), p, t.TempDir(), "0.1.0", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := release.Build(context.Background(), p, t.TempDir(), "0.1.0", nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if first.Source.SHA256 != second.Source.SHA256 {
		t.Errorf("source archive differs between runs")
	}
	for i, a := range first.Manifest.Artifacts {
		b := second.Manifest.Artifacts[i]
		if a.SHA256 != b.SHA256 {
			t.Errorf("%s differs between runs:\n  %s\n  %s", a.Name, a.SHA256, b.SHA256)
		}
	}
}

func TestBuildRefusesAFailedPlan(t *testing.T) {
	p := fixture(t)
	p.Checks = append(p.Checks, plan.Check{Name: "invented", Status: plan.Fail, Detail: "for the test"})

	if _, err := release.Build(context.Background(), p, t.TempDir(), "0.1.0", nil, nil); err == nil {
		t.Error("Build proceeded despite a failed gate")
	}
}

// caskFixturePlugin writes a shell-script stand-in for a tap-files plugin
// that answers with a single file, and returns the letsgo.mod line pinning
// it under the name cask-fixture.
func caskFixturePlugin(t *testing.T, path, content string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake plugin is a shell script")
	}
	scriptDir := t.TempDir()
	script := filepath.Join(scriptDir, "cask-fixture")
	body := fmt.Sprintf("#!/bin/sh\ncat > /dev/null\necho '{\"files\":[{\"path\":%q,\"content\":%q}]}'\n", path, content)
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return "plugin tap-files cask-fixture v0.1.0 sha256:" + hex.EncodeToString(sum[:])
}

// A release with a tap-files plugin pinned writes what it rendered into the
// manifest, without publishing anything: publishing is a separate call, made
// once the release's assets exist to be linked to.
func TestBuildRecordsTapFiles(t *testing.T) {
	pin := caskFixturePlugin(t, "Casks/demo.rb", "cask demo")
	p := fixture(t, "brew you/tap", pin)

	result, err := release.Build(context.Background(), p, t.TempDir(), "0.1.0", nil, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if len(result.TapFiles) != 1 || result.TapFiles[0].Path != "Casks/demo.rb" {
		t.Fatalf("result.TapFiles = %+v", result.TapFiles)
	}

	wantSum := sha256.Sum256([]byte("cask demo"))
	want := hex.EncodeToString(wantSum[:])
	if len(result.Manifest.TapFiles) != 1 ||
		result.Manifest.TapFiles[0].Path != "Casks/demo.rb" || result.Manifest.TapFiles[0].SHA256 != want {
		t.Errorf("manifest.TapFiles = %+v", result.Manifest.TapFiles)
	}
}

// A tap-files plugin that reaches outside Casks/ must stop the release before
// the manifest — or anything else — is written to disk.
func TestBuildFailsOnAnInvalidTapFilePath(t *testing.T) {
	pin := caskFixturePlugin(t, "../Formula/x.rb", "x")
	p := fixture(t, "brew you/tap", pin)

	if _, err := release.Build(context.Background(), p, t.TempDir(), "0.1.0", nil, nil); err == nil ||
		!strings.Contains(err.Error(), "not a valid tap path") {
		t.Errorf("err = %v", err)
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
