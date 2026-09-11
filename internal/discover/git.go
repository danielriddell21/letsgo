package discover

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Git describes the state of the repository at HEAD.
type Git struct {
	// Commit is the full HEAD SHA.
	Commit string

	// ShortCommit is the abbreviated SHA.
	ShortCommit string

	// CommitTime is HEAD's committer timestamp.
	//
	// This is the only clock a release is permitted to read. It becomes
	// SOURCE_DATE_EPOCH, every archive entry's modification time, and the date
	// injected into the binary — which is what makes rebuilding the same
	// commit tomorrow produce the same bytes as building it today.
	CommitTime time.Time

	// Tags are the tags pointing at HEAD, if any.
	Tags []string

	// Clean reports whether the worktree has no uncommitted changes.
	Clean bool

	// Shallow reports whether this is a shallow clone.
	//
	// Rather than demanding fetch-depth: 0 and failing cryptically without it,
	// letsgo records the fact and lets the changelog fall back to the forge's
	// compare API.
	Shallow bool
}

// FindGit inspects the repository containing dir.
func FindGit(ctx context.Context, dir string) (Git, error) {
	commit, err := git(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return Git{}, fmt.Errorf("discover: %s is not a git repository with any commits: %w", dir, err)
	}

	short, err := git(ctx, dir, "rev-parse", "--short", "HEAD")
	if err != nil {
		return Git{}, err
	}

	stamp, err := git(ctx, dir, "show", "-s", "--format=%cI", "HEAD")
	if err != nil {
		return Git{}, err
	}
	commitTime, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return Git{}, fmt.Errorf("discover: parsing commit time %q: %w", stamp, err)
	}

	status, err := git(ctx, dir, "status", "--porcelain")
	if err != nil {
		return Git{}, err
	}

	var tags []string
	if out, err := git(ctx, dir, "tag", "--points-at", "HEAD"); err == nil && out != "" {
		tags = strings.Split(out, "\n")
	}

	// A missing shallow marker is the normal case, so an error here means "not
	// shallow" rather than a failure worth reporting.
	shallow := false
	if out, err := git(ctx, dir, "rev-parse", "--is-shallow-repository"); err == nil {
		shallow = out == "true"
	}

	return Git{
		Commit:      commit,
		ShortCommit: short,
		CommitTime:  commitTime.UTC(),
		Tags:        tags,
		Clean:       status == "",
		Shallow:     shallow,
	}, nil
}

// PreviousTag returns the most recent tag reachable from HEAD, excluding any
// tag that points at HEAD itself. It is empty when there is no earlier tag,
// which is the normal state for a first release.
func PreviousTag(ctx context.Context, dir string) (string, error) {
	args := []string{"describe", "--tags", "--abbrev=0"}

	if current, err := git(ctx, dir, "tag", "--points-at", "HEAD"); err == nil {
		for _, tag := range strings.Split(current, "\n") {
			if tag != "" {
				args = append(args, "--exclude", tag)
			}
		}
	}

	out, err := git(ctx, dir, args...)
	if err != nil {
		// git describe exits non-zero when no tag exists. That is an ordinary
		// state, not a failure.
		return "", nil
	}
	return out, nil
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir

	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			return "", fmt.Errorf("discover: git %s: %w", strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("discover: git %s: %w: %s", strings.Join(args, " "), err, detail)
	}
	return strings.TrimSpace(string(out)), nil
}
