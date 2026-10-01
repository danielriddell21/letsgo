package gate

import "github.com/danielriddell21/letsgo/internal/sumdb"

// SumdbInput is what decides whether the sum.golang.org cross-check runs.
// Plan and publication both fill it in, so neither can disagree about the
// answer.
type SumdbInput struct {
	// Disabled is `disable sumdb`.
	Disabled bool
	// Snapshot is a rehearsal, and Untagged a run with no tag to look up.
	Snapshot, Untagged bool
	// Draft is a release nobody can see yet, so there is no public record.
	Draft bool
	// Scoped is a module that is not at the repository root.
	Scoped bool
	// ProxyWarmOff is `disable proxy-warm`: with the proxy not primed there is
	// nothing for sum.golang.org to be compared against.
	ProxyWarmOff bool
	// ModulePath is checked for being a private module.
	ModulePath string
}

// SumdbDecision is whether the cross-check runs and, when it does not, why.
type SumdbDecision struct {
	Run bool
	// ByConfig is set when the check is off because the repository said so,
	// which `require sumdb` cannot contradict: validation already refuses
	// that pairing.
	ByConfig bool
	Reason   string
}

// DecideSumdb is the one place that says whether the sum.golang.org check
// runs for a release.
func DecideSumdb(in SumdbInput) SumdbDecision {
	switch {
	case in.Disabled:
		return SumdbDecision{ByConfig: true, Reason: "disabled by config"}
	case in.Snapshot || in.Untagged:
		return SumdbDecision{Reason: "not a tagged release"}
	case in.Draft:
		return SumdbDecision{Reason: "a draft release is not public yet"}
	case in.Scoped:
		return SumdbDecision{Reason: "the module is not at the repository root"}
	case in.ProxyWarmOff:
		return SumdbDecision{Reason: "proxy-warm is disabled, so sum.golang.org has no record to compare"}
	}
	if private, why := sumdb.PrivateModule(in.ModulePath); private {
		return SumdbDecision{Reason: "private module (" + why + ")"}
	}
	return SumdbDecision{Run: true}
}
