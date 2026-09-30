package publication

import (
	"fmt"
	"slices"
)

// Step is one stage of a publication, in the order Publish runs them.
type Step string

// The stages of a publication.
const (
	StepGate    Step = "gate"
	StepRelease Step = "release"
	StepTap     Step = "tap"
	StepImages  Step = "images"
)

// StepError is a publication that stopped part way. Running the same command
// again resumes it: the forge release is adopted and its assets re-verified,
// and the tap and images are written only where they differ.
type StepError struct {
	// Step is the stage that failed.
	Step Step

	// Done are the stages that completed before it.
	Done []Step

	Err error
}

func (e *StepError) Error() string {
	if slices.Contains(e.Done, StepRelease) {
		return fmt.Sprintf("the %s failed after the release was published; run the command again to resume: %v", e.Step, e.Err)
	}
	return fmt.Sprintf("the %s failed: %v", e.Step, e.Err)
}

func (e *StepError) Unwrap() error { return e.Err }
