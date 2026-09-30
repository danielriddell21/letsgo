package plan

import "fmt"

// A release action has no fingerprint of its own, so its states are named.
const (
	releaseAbsent  = "absent"
	releaseCurrent = "current"
	releaseEdited  = "edited"
)

// Before is the state the action found its target in, as a fingerprint.
func (a Action) Before() string {
	if a.Kind == KindRelease {
		switch a.Op {
		case Add:
			return releaseAbsent
		case Change:
			return releaseEdited
		case Keep, Remove:
		}
		return releaseCurrent
	}
	if a.Op == Keep {
		return a.Planned
	}
	return a.Observed
}

// After is the state the action leaves its target in.
func (a Action) After() string {
	if a.Kind == KindRelease {
		return releaseCurrent
	}
	return a.Planned
}

// Drift is a target that is in neither the state a plan found it in nor the
// state it would leave it in.
type Drift struct {
	Kind   Kind
	Target string

	// Found is the target's state now; Was and Would are the states the plan
	// recorded before and after acting on it.
	Found, Was, Would string
}

func (d Drift) String() string {
	return fmt.Sprintf("%s %s is %s; the plan saw %s and would leave %s",
		d.Kind, d.Target, short(d.Found), short(d.Was), short(d.Would))
}

// Drifted compares a saved plan with a fresh observation of the same targets.
//
// A target is fine where it was, and fine where the plan would have put it: the
// second is what a half-finished apply leaves behind, and it is not drift.
// Anything else was changed by someone else since the plan was made. A target
// the fresh observation no longer mentions has nothing to disagree with.
func Drifted(saved, current []Action) []Drift {
	now := make(map[string]Action, len(current))
	for _, a := range current {
		now[key(a)] = a
	}

	var out []Drift
	for _, a := range saved {
		c, ok := now[key(a)]
		if !ok {
			continue
		}
		found := c.Before()
		if found == a.Before() || found == a.After() {
			continue
		}
		out = append(out, Drift{Kind: a.Kind, Target: a.Target, Found: found, Was: a.Before(), Would: a.After()})
	}
	return out
}

// Pending is the targets a plan would write to, by kind and target: everything
// that is not left as it is.
func Pending(actions []Action) map[Kind]map[string]Op {
	out := map[Kind]map[string]Op{}
	for _, a := range actions {
		if a.Op == Keep {
			continue
		}
		if out[a.Kind] == nil {
			out[a.Kind] = map[string]Op{}
		}
		out[a.Kind][a.Target] = a.Op
	}
	return out
}

func key(a Action) string { return string(a.Kind) + "\x00" + a.Target }
