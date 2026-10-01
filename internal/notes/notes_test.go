package notes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/feature"
	"github.com/danielriddell21/letsgo/internal/notes/notestest"
	"github.com/danielriddell21/letsgo/internal/pgpwords"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/manifest"
)

var repo = github.Repo{Owner: "you", Name: "demo"}

func manifestOf(version, goVersion string) *manifest.Manifest {
	return &manifest.Manifest{
		Schema: manifest.Schema, Version: version, Builder: manifest.Builder{Tool: "letsgo", Go: goVersion},
	}
}

// disable changelog must stop the changelog from being built at all, not
// merely from being shown: a nil client proves this returns before it would
// have made a network call.
func TestReleaseSkippedWhenChangelogDisabled(t *testing.T) {
	p := &plan.Plan{GitBin: "git", Features: feature.Resolve([]string{"changelog"})}

	got, err := Release(context.Background(), Source{Plan: p})
	if err != nil || got != "" {
		t.Errorf("Release = (%q, %v), want empty and no error", got, err)
	}
}

// A first release has no previous tag to diff against, so there is nothing
// to fetch: whatShipped must return before it would have made a network call.
func TestWhatShippedSkipsAFirstRelease(t *testing.T) {
	s := Source{Plan: &plan.Plan{GitBin: "git"}, Manifest: &manifest.Manifest{Version: "v1.0.0"}}
	out, err := s.whatShipped(context.Background(), "")
	if err != nil || out != "" {
		t.Errorf("whatShipped = %q, %v, want empty for a first release", out, err)
	}
}

// A previous release published before letsgo recorded a manifest leaves
// nothing to fetch; the release must still go out, just without this section.
func TestWhatShippedSkipsAPreviousReleaseWithNoManifest(t *testing.T) {
	var logged []string
	s := Source{
		Plan:     &plan.Plan{GitBin: "git"},
		Client:   notestest.Forge(t, "you/demo", "v1.0.0", nil), // no manifest: no asset to find
		Repo:     repo,
		Manifest: manifestOf("v1.1.0", "go1.26.2"),
		Logf:     func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) },
	}

	out, err := s.whatShipped(context.Background(), "v1.0.0")
	if err != nil {
		t.Errorf("whatShipped: %v", err)
	}
	if out != "" {
		t.Errorf("whatShipped = %q, want empty", out)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], `skipped the "what shipped" section`) {
		t.Errorf("logged = %q, want a skip notice", logged)
	}
}

// The skip notice is a courtesy: a caller with nowhere to put it still ships.
func TestWhatShippedSkipsQuietlyWithoutALogger(t *testing.T) {
	s := Source{
		Plan: &plan.Plan{GitBin: "git"}, Client: notestest.Forge(t, "you/demo", "v1.0.0", nil), Repo: repo,
		Manifest: manifestOf("v1.1.0", "go1.26.2"),
	}
	if out, err := s.whatShipped(context.Background(), "v1.0.0"); err != nil || out != "" {
		t.Errorf("whatShipped = %q, %v, want empty", out, err)
	}
}

// A release that required diff-notes asked for the failure the tests above
// tolerate: no previous release, or one with no manifest, is an error.
func TestWhatShippedFailsWhenRequiredAndNothingToCompare(t *testing.T) {
	s := Source{Plan: &plan.Plan{GitBin: "git", Required: []string{"diff-notes"}}, Repo: repo, Manifest: &manifest.Manifest{Version: "v1.1.0"}}

	if _, err := s.whatShipped(context.Background(), ""); err == nil {
		t.Error("a first release should fail when diff-notes is required")
	}

	s.Client = notestest.Forge(t, "you/demo", "v1.0.0", nil)
	if _, err := s.whatShipped(context.Background(), "v1.0.0"); err == nil {
		t.Error("a previous release with no manifest should fail when diff-notes is required")
	}
}

func TestWhatShippedRendersTheCollapsedSection(t *testing.T) {
	s := Source{
		Plan:     &plan.Plan{GitBin: "git"},
		Client:   notestest.Forge(t, "you/demo", "v1.0.0", manifestOf("v1.0.0", "go1.26.1")),
		Repo:     repo,
		Manifest: manifestOf("v1.1.0", "go1.26.2"),
	}

	out, err := s.whatShipped(context.Background(), "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<details><summary>What shipped (vs v1.0.0)</summary>") {
		t.Errorf("whatShipped = %q, want the collapsed summary", out)
	}
	if !strings.Contains(out, "go1.26.1 → go1.26.2") {
		t.Errorf("whatShipped = %q, want the toolchain row", out)
	}
}

// The notes section must compare against the same previous release the
// changelog above it just used, and must be appended after it.
func TestReleaseAppendsWhatShippedUsingTheChangelogsPreviousRelease(t *testing.T) {
	p := &plan.Plan{
		GitBin:   "git",
		Features: feature.Resolve(nil),
		Module:   discover.Module{Dir: notestest.History(t)},
		Tag:      "v1.1.0",
	}
	got, err := Release(context.Background(), Source{
		Plan:     p,
		Client:   notestest.Forge(t, "you/demo", "v1.0.0", manifestOf("v1.0.0", "go1.26.1")),
		Repo:     repo,
		Manifest: manifestOf("v1.1.0", "go1.26.2"),
	})
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if !strings.Contains(got, "second release") {
		t.Errorf("notes = %q, want the changelog entry", got)
	}
	if !strings.Contains(got, "<details><summary>What shipped (vs v1.0.0)</summary>") {
		t.Errorf("notes = %q, want the what-shipped section against v1.0.0", got)
	}
	if strings.Index(got, "second release") > strings.Index(got, "What shipped") {
		t.Errorf("notes = %q, want the what-shipped section after the changelog", got)
	}
}

// disable diff-notes must remove the section, and must do so before the
// forge is ever asked for the previous manifest.
func TestReleaseOmitsWhatShippedWhenDisabled(t *testing.T) {
	p := &plan.Plan{
		GitBin:   "git",
		Features: feature.Resolve([]string{"diff-notes"}),
		Module:   discover.Module{Dir: notestest.History(t)},
		Tag:      "v1.1.0",
	}
	got, err := Release(context.Background(), Source{Plan: p, Manifest: manifestOf("v1.1.0", "go1.26.2")})
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if !strings.Contains(got, "second release") {
		t.Errorf("notes = %q, want the changelog entry", got)
	}
	if strings.Contains(got, "What shipped") {
		t.Errorf("notes = %q, want no what-shipped section", got)
	}
}

func TestReleaseEndsWithTheManifestFingerprint(t *testing.T) {
	p := &plan.Plan{
		GitBin:   "git",
		Features: feature.Resolve([]string{"diff-notes"}),
		Module:   discover.Module{Dir: notestest.History(t)},
		Tag:      "v1.1.0",
	}
	sum := sha256.Sum256([]byte("manifest"))

	got, err := Release(context.Background(), Source{Plan: p, ManifestSum: sum[:]})
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	for _, want := range []string{
		"<summary>Manifest fingerprint</summary>", "[letsgo v1.1.0]", "sha256:" + hex.EncodeToString(sum[:]),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("notes = %q, want %q", got, want)
		}
	}
	// PW-6: the words in the body are the ones `verify --words` prints.
	if want := pgpwords.Rows(pgpwords.Encode(sum[:])); !strings.Contains(got, want) {
		t.Errorf("notes = %q, want the words %q", got, want)
	}
	if strings.Index(got, "second release") > strings.Index(got, "Manifest fingerprint") {
		t.Errorf("notes = %q, want the fingerprint after the changelog", got)
	}
}

func TestReleaseOmitsTheFingerprintWhenDisabled(t *testing.T) {
	p := &plan.Plan{
		GitBin:   "git",
		Features: feature.Resolve([]string{"diff-notes", "randomart"}),
		Module:   discover.Module{Dir: notestest.History(t)},
		Tag:      "v1.1.0",
	}
	sum := sha256.Sum256([]byte("manifest"))

	got, err := Release(context.Background(), Source{Plan: p, ManifestSum: sum[:]})
	if err != nil {
		t.Fatalf("Release: %v", err)
	}
	if strings.Contains(got, "fingerprint") {
		t.Errorf("notes = %q, want no fingerprint", got)
	}
}

// Without a digest there is nothing to draw, which only matters to a release
// that required the drawing.
func TestExtraFailsWhenRandomartIsRequiredAndThereIsNoDigest(t *testing.T) {
	p := &plan.Plan{GitBin: "git", Features: feature.Resolve([]string{"diff-notes"}), Required: []string{"randomart"}}
	if _, err := Extra(context.Background(), Source{Plan: p}, ""); err == nil {
		t.Error("a required fingerprint with no digest should fail")
	}

	p.Required = nil
	if got, err := Extra(context.Background(), Source{Plan: p}, ""); err != nil || got != "" {
		t.Errorf("Extra = (%q, %v), want empty and no error", got, err)
	}
}

// A failure the changelog cannot recover from, or a required section that
// cannot be built, stops the release rather than shipping thinner notes.
func TestReleasePropagatesFailures(t *testing.T) {
	notRepo := &plan.Plan{GitBin: "git", Features: feature.Resolve(nil), Module: discover.Module{Dir: t.TempDir()}, Tag: "v1.1.0"}
	if _, err := Release(context.Background(), Source{Plan: notRepo}); err == nil {
		t.Error("a module with no history should fail")
	}

	required := &plan.Plan{
		GitBin:   "git",
		Features: feature.Resolve([]string{"randomart"}),
		Required: []string{"diff-notes"},
		Module:   discover.Module{Dir: notestest.History(t)},
		Tag:      "v1.1.0",
	}
	s := Source{Plan: required, Client: notestest.Forge(t, "you/demo", "v1.0.0", nil), Repo: repo}
	if _, err := Release(context.Background(), s); err == nil {
		t.Error("a required diff-notes with nothing to compare should fail")
	}
	if _, err := Extra(context.Background(), s, "v1.0.0"); err == nil {
		t.Error("Extra should return the required section's failure")
	}
}

// A shallow clone has no history to read, so the notes say where it comes from.
func TestReleaseSaysWhenItReadsAShallowCloneFromTheForge(t *testing.T) {
	p := &plan.Plan{GitBin: "git", Features: feature.Resolve(nil), Module: discover.Module{Dir: t.TempDir()}, Tag: "v1.1.0"}
	p.Git.Shallow = true
	var logged []string
	_, _ = Release(context.Background(), Source{
		Plan: p, Client: notestest.Forge(t, "you/demo", "v1.0.0", nil), Repo: repo,
		Logf: func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) },
	})
	if len(logged) == 0 || !strings.Contains(logged[0], "shallow clone") {
		t.Errorf("logged = %q, want the shallow-clone notice", logged)
	}
}
