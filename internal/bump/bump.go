// Package bump works out which version a release should carry.
//
// Two signals are available and neither subsumes the other. The exported API
// diff answers whether existing code will still compile, which is a fact the
// compiler can settle. Commit messages answer what the change was meant to be,
// which covers everything the Go API cannot see — a removed command-line flag,
// an altered default, a different output format.
//
// They combine by taking the higher, because each can only establish a floor.
// An incompatible API change proves a major version is needed; an unchanged
// API proves nothing at all, since behaviour behind an identical signature is
// free to change completely. Neither signal can talk the other down.
package bump

import (
	"fmt"
	"strings"

	"github.com/danielriddell21/letsgo/internal/changelog"
	"github.com/danielriddell21/letsgo/internal/gate"
	"github.com/danielriddell21/letsgo/internal/semver"
)

// Level is the size of a version change.
type Level int

const (
	// None means a signal had nothing to say, which is different from saying
	// that nothing changed.
	None Level = iota
	Patch
	Minor
	Major
)

func (l Level) String() string {
	switch l {
	case Patch:
		return "patch"
	case Minor:
		return "minor"
	case Major:
		return "major"
	default:
		return "none"
	}
}

// Signal is one source's view of how large the change is.
type Signal struct {
	Source string
	Level  Level
	Detail string
}

// Proposal is a recommended next version and the evidence for it.
type Proposal struct {
	Previous string
	Next     string
	Level    Level
	Signals  []Signal

	// Notes carry anything the caller must act on beyond tagging, such as a
	// module path that has to change first.
	Notes []string
}

// FromAPI reads the exported API delta.
//
// Only ever a floor: additions cannot break a dependant, removals always can,
// and an unchanged API says nothing about behaviour.
func FromAPI(changes []gate.Change, available bool) Signal {
	if !available {
		return Signal{
			Source: "exported API", Level: None,
			Detail: "not compared; nothing in this module is importable, or apidiff is not installed",
		}
	}

	if breaking := gate.Incompatibles(changes); len(breaking) > 0 {
		return Signal{
			Source: "exported API", Level: Major,
			Detail: plural(len(breaking), "incompatible change"),
		}
	}
	if len(changes) > 0 {
		return Signal{
			Source: "exported API", Level: Minor,
			Detail: fmt.Sprintf("%s, all additions", plural(len(changes), "change")),
		}
	}
	return Signal{Source: "exported API", Level: None, Detail: "unchanged"}
}

// FromCommits reads conventional-commit types.
func FromCommits(entries []changelog.Entry) Signal {
	level, reasons := None, map[string]int{}

	for _, e := range entries {
		var entryLevel Level
		switch {
		case e.Breaking:
			entryLevel = Major
		case e.Type == "feat":
			entryLevel = Minor
		case e.Type == "fix" || e.Type == "perf" || e.Type == "revert":
			entryLevel = Patch
		default:
			// An unlabelled commit is not evidence of a small change; it is
			// the absence of evidence.
			continue
		}

		reasons[entryLevel.String()]++
		level = max(level, entryLevel)
	}

	if level == None {
		return Signal{
			Source: "commit messages", Level: None,
			Detail: "nothing conventionally labelled",
		}
	}

	var parts []string
	for _, name := range []string{"major", "minor", "patch"} {
		if n := reasons[name]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, name))
		}
	}
	return Signal{Source: "commit messages", Level: level, Detail: strings.Join(parts, ", ")}
}

// Propose computes the next version from the previous one and the signals.
func Propose(previous, modulePath string, signals ...Signal) (Proposal, error) {
	p := Proposal{Previous: previous, Signals: signals}

	for _, s := range signals {
		p.Level = max(p.Level, s.Level)
	}

	// A release nobody has a signal for is still a release; the smallest
	// honest claim is a patch.
	if p.Level == None {
		p.Level = Patch
	}

	if previous == "" {
		// A first release is not a bump. v0.1.0 rather than v0.0.1 or v1.0.0:
		// it claims a usable thing exists without promising stability.
		p.Next = "v0.1.0"
		p.Notes = append(p.Notes, "first release; no earlier tag to compare against")
		return p, nil
	}

	from, ok := semver.Parse(previous)
	if !ok {
		return Proposal{}, fmt.Errorf("bump: previous tag %q is not a version", previous)
	}

	level := p.Level

	// Below v1 there is no compatibility promise to break, and the convention
	// is that minor absorbs what would otherwise be a major. Promoting to
	// v1.0.0 on the first removal would commit the project to stability it has
	// not claimed.
	if from.Major == 0 && level == Major {
		level = Minor
		p.Notes = append(p.Notes,
			"breaking, but v0 makes no compatibility promise, so the minor is bumped instead of reaching v1.0.0")
	}

	// Components rather than a copy of `from`: the next version is a release,
	// so it carries neither the prerelease nor the build metadata the previous
	// tag may have had, and clearing them on a copy only to overwrite the rest
	// says less than not carrying them at all.
	major, minor, patch := from.Major, from.Minor, from.Patch
	switch level {
	case Major:
		major, minor, patch = major+1, 0, 0
	case Minor:
		minor, patch = minor+1, 0
	default:
		patch++
	}

	p.Next = fmt.Sprintf("v%d.%d.%d", major, minor, patch)

	// A major version above v1 lives at a different import path. Tagging it
	// without moving the module first produces a release `go get` resolves
	// straight past.
	if major >= 2 && major != from.Major {
		if !strings.HasSuffix(modulePath, fmt.Sprintf("/v%d", major)) {
			p.Notes = append(p.Notes, fmt.Sprintf(
				"%s needs the module path to end /v%d first; change go.mod, commit, then tag",
				p.Next, major))
		}
	}

	return p, nil
}

// Disagree reports whether the signals reached different conclusions, which is
// worth showing: a removal labelled as a fix is a mislabelling, and a breaking
// commit with an unchanged API is usually a command-line change the diff
// cannot see.
func (p Proposal) Disagree() bool {
	var seen Level
	first := true
	for _, s := range p.Signals {
		if s.Level == None {
			continue
		}
		if first {
			seen, first = s.Level, false
			continue
		}
		if s.Level != seen {
			return true
		}
	}
	return false
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
