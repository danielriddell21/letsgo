package publication

import (
	"errors"
	"strings"
	"testing"
)

func TestStepErrorSaysHowToResumeOnceTheReleaseIsPublished(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  *StepError
		want string
	}{
		{"before the release", &StepError{Step: StepGate, Err: errors.New("boom")}, "the gate failed: boom"},
		{"after the release", &StepError{Step: StepTap, Done: []Step{StepGate, StepRelease}, Err: errors.New("boom")}, "run the command again to resume: boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.err.Error(); !strings.Contains(got, tt.want) {
				t.Errorf("Error() = %q, want it to contain %q", got, tt.want)
			}
		})
	}
}

func TestStepErrorUnwrapsToTheCause(t *testing.T) {
	t.Parallel()
	cause := errors.New("boom")
	if err := error(&StepError{Step: StepImages, Err: cause}); !errors.Is(err, cause) {
		t.Error("errors.Is did not find the cause through the StepError")
	}
}
