package brew

import (
	"fmt"
	"strings"

	"github.com/danielriddell21/letsgo/internal/publish/github"
)

// prefix is the naming convention Homebrew enforces: `brew tap you/foo` looks
// for a repository called `homebrew-foo`.
const prefix = "homebrew-"

// ParseTap reads a tap reference such as "you/homebrew-tap" or "you/tap".
//
// Both spellings are accepted because both are things people write: the second
// is what `brew tap` takes, and rejecting it would be pedantry about a
// convention letsgo can simply apply.
func ParseTap(s string) (github.Repo, error) {
	owner, name, ok := strings.Cut(strings.TrimSpace(s), "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return github.Repo{}, fmt.Errorf("brew: tap %q must be owner/repo", s)
	}
	if !strings.HasPrefix(name, prefix) {
		name = prefix + name
	}
	return github.Repo{Owner: owner, Name: name}, nil
}
