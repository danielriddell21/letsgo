// Package plan is what a release plan says it will do.
//
// A plan is a list of actions, one per thing a release touches: the release
// itself, each asset, the Homebrew tap's files, each container image tag, and
// the module proxy. Each action compares what was observed on the forge with
// what the release intends, so a reviewer reads a diff rather than a script.
//
// The package has no dependency on the rest of letsgo, so anything that reads
// a plan pulls in nothing beyond what it already needs.
package plan

import (
	"fmt"
	"strings"
)

// Op is what an action does to its target.
type Op string

const (
	// Add creates a target that does not exist.
	Add Op = "+"

	// Change replaces a target that exists and differs.
	Change Op = "~"

	// Remove deletes a target that exists.
	Remove Op = "-"

	// Keep leaves a target that is already as intended.
	Keep Op = "="
)

// Kind is the sort of target an action touches.
type Kind string

// The kinds of target a release touches.
const (
	KindRelease Kind = "release"
	KindAsset   Kind = "asset"
	KindTap     Kind = "tap"
	KindImage   Kind = "image"
	KindProxy   Kind = "proxy"
	KindGoMod   Kind = "gomod"
)

// Kinds is the closed set of kinds, so a consumer that maps each one to
// something (the Terraform export) can test that its table misses none.
var Kinds = []Kind{KindRelease, KindAsset, KindTap, KindImage, KindProxy, KindGoMod}

// Action is one line of a plan.
//
// Observed and Planned are fingerprints of the target's state, not the state
// itself: an asset's digest, a tap file's blob sha, an image's digest, or
// empty for a target that is absent. Comparing them is how a plan decides
// what to do, and, later, whether it has gone stale.
type Action struct {
	Op       Op     `json:"op"`
	Kind     Kind   `json:"kind"`
	Target   string `json:"target"`
	Observed string `json:"observed"`
	Planned  string `json:"planned"`
}

// Counts returns how many actions add, change and remove. Actions that keep
// their target are not counted: nothing happens to them.
func Counts(actions []Action) (add, change, remove int) {
	for _, a := range actions {
		switch a.Op {
		case Add:
			add++
		case Change:
			change++
		case Remove:
			remove++
		case Keep:
		}
	}
	return add, change, remove
}

// Summary is the footer a rendered plan ends with.
func Summary(actions []Action) string {
	add, change, remove := Counts(actions)
	return fmt.Sprintf("Plan: %d to add, %d to change, %d to remove.", add, change, remove)
}

// Render draws the actions one to a line, in the order given, followed by the
// footer.
func Render(actions []Action) string {
	kindWidth, targetWidth := 0, 0
	for _, a := range actions {
		kindWidth = max(kindWidth, len(a.Kind))
		targetWidth = max(targetWidth, len(a.Target))
	}

	var b strings.Builder
	for _, a := range actions {
		fmt.Fprintf(&b, "  %s %-*s  %-*s  %s\n", a.Op, kindWidth, a.Kind, targetWidth, a.Target, detail(a))
	}
	if len(actions) > 0 {
		b.WriteString("\n")
	}
	b.WriteString("  " + Summary(actions) + "\n")
	return b.String()
}

// detail says what changes about a target, in the shortest form that still
// distinguishes the states.
func detail(a Action) string {
	switch a.Op {
	case Keep:
		return "(already " + short(a.Planned) + ")"
	case Change:
		return short(a.Observed) + " → " + short(a.Planned)
	case Add:
		return short(a.Planned)
	case Remove:
		return short(a.Observed)
	}
	return ""
}

// short abbreviates a fingerprint such as sha256:<hex> to its first few
// characters, which is enough to tell two apart on a screen.
func short(fingerprint string) string {
	if fingerprint == "" {
		return "absent"
	}
	algo, value, ok := strings.Cut(fingerprint, ":")
	if !ok || len(value) <= 12 {
		return fingerprint
	}
	return algo + ":" + value[:4] + "…"
}
