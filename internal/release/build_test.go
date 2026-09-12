package release_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/release"
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

func fixture(t *testing.T) *plan.Plan {
	t.Helper()
	dir := t.TempDir()

	write := func(name, content string) {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/demo\n\ngo 1.24\n")
	write("main.go", mainGo)
	write("README.md", "# demo\n")
	write("letsgo.mod", "build "+gobuild.Host().String()+"\n")

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
	run("add", ".")
	run("commit", "-q", "-m", "feat: first release")
	run("tag", "v1.2.3")

	p, err := plan.Resolve(context.Background(), plan.Options{Dir: dir})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !p.OK() {
		t.Fatalf("plan did not pass: %+v", p.Checks)
	}
	return p
}

func TestBuildProducesACompleteRelease(t *testing.T) {
	p := fixture(t)
	dir := t.TempDir()

	result, err := release.Build(context.Background(), p, dir, "0.1.0", nil)
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

// The manifest's digests are what verification and resumable upload both rely
// on. If they do not describe the bytes on disk, everything downstream is
// quietly wrong.
func TestManifestDigestsMatchTheFilesOnDisk(t *testing.T) {
	p := fixture(t)
	dir := t.TempDir()

	result, err := release.Build(context.Background(), p, dir, "0.1.0", nil)
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

	result, err := release.Build(context.Background(), p, dir, "0.1.0", nil)
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

	first, err := release.Build(context.Background(), p, t.TempDir(), "0.1.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := release.Build(context.Background(), p, t.TempDir(), "0.1.0", nil)
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

	if _, err := release.Build(context.Background(), p, t.TempDir(), "0.1.0", nil); err == nil {
		t.Error("Build proceeded despite a failed gate")
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
