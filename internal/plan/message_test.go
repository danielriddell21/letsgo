package plan

import (
	"strings"
	"testing"
)

// Under Actions the permission cannot be read, so the message must not claim
// it is absent — it must say what is unknown and where to look if the publish
// then fails.
func TestUnconfirmedMessageDoesNotAssertAbsence(t *testing.T) {
	msg := unconfirmedUnderActions("you/foo")

	for _, want := range []string{"could not be confirmed", "not evidence", "Workflow permissions"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message is missing %q:\n%s", want, msg)
		}
	}
	// Asserting the token cannot write would be a claim the API did not make.
	for _, wrong := range []string{"cannot write", "has no effect"} {
		if strings.Contains(msg, wrong) {
			t.Errorf("message asserts something unproven (%q):\n%s", wrong, msg)
		}
	}
}

func TestUnderActionsReadsTheEnvironment(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	if !underActions() {
		t.Error("underActions is false inside Actions")
	}
	t.Setenv("GITHUB_ACTIONS", "")
	if underActions() {
		t.Error("underActions is true outside Actions")
	}
}

// Outside Actions the reported permission is trustworthy, so the message can
// name the fix directly.
func TestNoWriteAccessNamesTokenScopes(t *testing.T) {
	msg := noWriteAccess("GITHUB_TOKEN", "you/foo")
	for _, want := range []string{"contents:write", "fine-grained", "repo scope"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message is missing %q:\n%s", want, msg)
		}
	}
}
