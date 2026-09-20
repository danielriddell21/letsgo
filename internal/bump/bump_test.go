package bump

import (
	"errors"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/changelog"
	"github.com/danielriddell21/letsgo/internal/gate"
)

func TestFromAPI(t *testing.T) {
	tests := map[string]struct {
		changes []gate.Change
		err     error
		want    Level
	}{
		"a removal proves a major is needed": {
			changes: []gate.Change{{Kind: gate.Incompatible}}, want: Major,
		},
		"additions cannot break anyone": {
			changes: []gate.Change{{Kind: gate.Compatible}}, want: Minor,
		},
		// The crucial one: an unchanged API is not evidence of a small
		// change, because behaviour behind an identical signature is free to
		// change completely.
		"an unchanged API says nothing":          {want: None},
		"an unavailable comparison says nothing": {err: gate.ErrToolMissing, want: None},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := FromAPI(tt.changes, tt.err).Level; got != tt.want {
				t.Errorf("Level = %v, want %v", got, tt.want)
			}
		})
	}
}

// A signal that dropped out must say why it dropped out. Reporting every
// cause with one sentence that guesses between two of them sent a real
// failure — apidiff built by an older Go than the module targets — out as
// "apidiff is not installed", when it was installed and working.
func TestFromAPIReportsWhyItCouldNotCompare(t *testing.T) {
	tests := map[string]struct {
		err  error
		want string
	}{
		"the tool is missing":     {gate.ErrToolMissing, "apidiff is not installed"},
		"nothing is importable":   {gate.ErrNothingExported, "nothing in this module is importable"},
		"the tool ran and failed": {errors.New("package requires newer Go version go1.27"), "package requires newer Go version go1.27"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := FromAPI(nil, tt.err)
			if got.Level != None {
				t.Errorf("Level = %v, want None", got.Level)
			}
			if !strings.Contains(got.Detail, tt.want) {
				t.Errorf("Detail = %q, want it to mention %q", got.Detail, tt.want)
			}
			if !strings.HasPrefix(got.Detail, "not compared") {
				t.Errorf("Detail = %q, want it to start with \"not compared\"", got.Detail)
			}
		})
	}
}

func TestFromCommits(t *testing.T) {
	tests := map[string]struct {
		entries []changelog.Entry
		want    Level
	}{
		"a breaking change": {[]changelog.Entry{{Type: "feat", Breaking: true}}, Major},
		"a feature":         {[]changelog.Entry{{Type: "feat"}}, Minor},
		"a fix":             {[]changelog.Entry{{Type: "fix"}}, Patch},
		"the largest wins": {
			[]changelog.Entry{{Type: "fix"}, {Type: "feat"}, {Type: "fix"}}, Minor,
		},
		// An unlabelled commit is the absence of evidence, not evidence of
		// absence.
		"unlabelled commits": {[]changelog.Entry{{Subject: "did some things"}}, None},
		"nothing at all":     {nil, None},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := FromCommits(tt.entries).Level; got != tt.want {
				t.Errorf("Level = %v, want %v", got, tt.want)
			}
		})
	}
}

// Each signal establishes a floor; neither can lower the other.
func TestProposeTakesTheHigherSignal(t *testing.T) {
	tests := map[string]struct {
		api, commits Level
		previous     string
		want         string
	}{
		"api raises above the commits": {
			Major, Patch, "v1.2.3", "v2.0.0",
		},
		"commits raise above a silent api": {
			None, Minor, "v1.2.3", "v1.3.0",
		},
		"agreement": {
			Minor, Minor, "v1.2.3", "v1.3.0",
		},
		"neither has anything to say": {
			None, None, "v1.2.3", "v1.2.4",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			p, err := Propose(tt.previous, "example.com/foo/v2",
				Signal{Source: "exported API", Level: tt.api},
				Signal{Source: "commit messages", Level: tt.commits})
			if err != nil {
				t.Fatal(err)
			}
			if p.Next != tt.want {
				t.Errorf("Next = %s, want %s", p.Next, tt.want)
			}
		})
	}
}

// Below v1 there is no compatibility promise to break, so a breaking change
// bumps the minor rather than committing the project to 1.0.
func TestBreakingBelowV1BumpsMinor(t *testing.T) {
	p, err := Propose("v0.1.0", "example.com/foo", Signal{Level: Major})
	if err != nil {
		t.Fatal(err)
	}
	if p.Next != "v0.2.0" {
		t.Errorf("Next = %s, want v0.2.0", p.Next)
	}
	if len(p.Notes) == 0 || !strings.Contains(strings.Join(p.Notes, " "), "v0") {
		t.Errorf("the v0 rule was applied without explaining it: %v", p.Notes)
	}
}

func TestV0MinorAndPatchAreUnaffected(t *testing.T) {
	for level, want := range map[Level]string{Minor: "v0.2.0", Patch: "v0.1.1"} {
		p, err := Propose("v0.1.0", "example.com/foo", Signal{Level: level})
		if err != nil {
			t.Fatal(err)
		}
		if p.Next != want {
			t.Errorf("%v from v0.1.0 = %s, want %s", level, p.Next, want)
		}
	}
}

// Tagging v2 without moving the module path produces a release go get
// resolves straight past.
func TestMajorWarnsAboutTheModulePath(t *testing.T) {
	p, err := Propose("v1.9.0", "example.com/foo", Signal{Level: Major})
	if err != nil {
		t.Fatal(err)
	}
	if p.Next != "v2.0.0" {
		t.Fatalf("Next = %s", p.Next)
	}
	notes := strings.Join(p.Notes, " ")
	if !strings.Contains(notes, "/v2") || !strings.Contains(notes, "go.mod") {
		t.Errorf("no guidance about the module path: %v", p.Notes)
	}
}

func TestMajorWithAMatchingPathIsQuiet(t *testing.T) {
	p, err := Propose("v2.1.0", "example.com/foo/v3", Signal{Level: Major})
	if err != nil {
		t.Fatal(err)
	}
	if p.Next != "v3.0.0" {
		t.Fatalf("Next = %s", p.Next)
	}
	for _, note := range p.Notes {
		if strings.Contains(note, "go.mod") {
			t.Errorf("warned about a module path that is already correct: %q", note)
		}
	}
}

func TestFirstRelease(t *testing.T) {
	p, err := Propose("", "example.com/foo", Signal{Level: Minor})
	if err != nil {
		t.Fatal(err)
	}
	if p.Next != "v0.1.0" {
		t.Errorf("Next = %s, want v0.1.0", p.Next)
	}
}

// A pre-release is a staging post, not a component of the next number.
func TestPrereleaseIsDropped(t *testing.T) {
	p, err := Propose("v1.2.0-rc1", "example.com/foo", Signal{Level: Patch})
	if err != nil {
		t.Fatal(err)
	}
	if p.Next != "v1.2.1" {
		t.Errorf("Next = %s, want v1.2.1", p.Next)
	}
}

func TestProposeRejectsAnUnparseablePrevious(t *testing.T) {
	if _, err := Propose("release-7", "example.com/foo", Signal{Level: Patch}); err == nil {
		t.Error("an unparseable previous tag was accepted")
	}
}

// A disagreement is worth surfacing: a removal labelled as a fix is a
// mislabelling, and a breaking commit with an unchanged API is usually a
// command-line change the diff cannot see.
func TestDisagreementIsDetected(t *testing.T) {
	disagreeing := Proposal{Signals: []Signal{{Level: Major}, {Level: Patch}}}
	if !disagreeing.Disagree() {
		t.Error("a disagreement was not detected")
	}

	agreeing := Proposal{Signals: []Signal{{Level: Minor}, {Level: Minor}}}
	if agreeing.Disagree() {
		t.Error("agreement was reported as a disagreement")
	}

	// A silent signal is not a dissenting one.
	silent := Proposal{Signals: []Signal{{Level: Major}, {Level: None}}}
	if silent.Disagree() {
		t.Error("a silent signal was treated as disagreement")
	}
}

// An unattended tagger needs to tell "nothing claims to be a release" apart
// from "a patch was asked for", and the proposal's level cannot: with no
// signal at all it still reads patch, because someone asking for a version by
// hand should get one.
func TestSignalledSeparatesAPatchFromNoClaimAtAll(t *testing.T) {
	tests := []struct {
		name    string
		signals []Signal
		want    bool
	}{
		{
			name: "nothing labelled",
			signals: []Signal{
				{Source: "commit messages", Level: None},
				{Source: "exported API", Level: None},
			},
		},
		{
			name:    "no signals gathered at all",
			signals: nil,
		},
		{
			name: "a fix",
			signals: []Signal{
				{Source: "commit messages", Level: Patch},
				{Source: "exported API", Level: None},
			},
			want: true,
		},
		{
			name:    "asked for on the command line",
			signals: []Signal{{Source: "you", Level: Minor}},
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Propose("v1.2.3", "github.com/you/tool", tt.signals...)
			if err != nil {
				t.Fatalf("Propose() error = %v", err)
			}
			if got := p.Signalled(); got != tt.want {
				t.Errorf("Signalled() = %v, want %v (level %s, next %s)", got, tt.want, p.Level, p.Next)
			}
		})
	}
}
