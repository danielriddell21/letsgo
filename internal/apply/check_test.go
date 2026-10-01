package apply

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/plan"
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

func TestAgreedAcceptsAnExactRebuild(t *testing.T) {
	body := `{"version":"1.3.0"}`
	result, digest := rebuiltIn(t, body)

	err := Agreed(planFileFor("v1.3.0", "abc", body, digest))(resolvedPlan("v1.3.0", "abc"), result)
	if err != nil {
		t.Errorf("agreedPlan = %v, want nil", err)
	}
}

func TestAgreedRefusesADifferentTagOrCommit(t *testing.T) {
	body := `{"version":"1.3.0"}`
	result, digest := rebuiltIn(t, body)
	file := planFileFor("v1.3.0", "abcdef0123456789", body, digest)

	if err := Agreed(file)(resolvedPlan("v1.4.0", "abcdef0123456789"), result); err == nil ||
		!strings.Contains(err.Error(), "v1.3.0") || !strings.Contains(err.Error(), "v1.4.0") {
		t.Errorf("different tag: %v", err)
	}
	if err := Agreed(file)(resolvedPlan("v1.3.0", "fedcba9876543210"), result); err == nil ||
		!strings.Contains(err.Error(), "abcdef012345") {
		t.Errorf("different commit: %v", err)
	}
}

func TestAgreedRefusesARebuildThatDiffersAndSaysWhere(t *testing.T) {
	planned := `{"version":"1.3.0","builder":{"go":"1.27.1"}}`
	_, plannedDigest := rebuiltIn(t, planned)
	result, _ := rebuiltIn(t, `{"version":"1.3.0","builder":{"go":"1.27.2"}}`)

	err := Agreed(planFileFor("v1.3.0", "abc", planned, plannedDigest))(resolvedPlan("v1.3.0", "abc"), result)
	if err == nil {
		t.Fatal("agreedPlan accepted a rebuild that differs")
	}
	for _, want := range []string{"does not match the plan", "nothing was published", "builder.go: 1.27.1 → 1.27.2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
}

func TestAgreedReportsAMissingManifest(t *testing.T) {
	file := planFileFor("v1.3.0", "abc", "{}", "sha256:x")
	if err := Agreed(file)(resolvedPlan("v1.3.0", "abc"), &release.Result{Dir: t.TempDir()}); err == nil {
		t.Error("agreedPlan succeeded with no manifest to compare")
	}
}
