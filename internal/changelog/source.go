package changelog

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/semver"
)

// Source describes where to read history from.
type Source struct {
	// Dir is the repository.
	Dir string

	// Tag is the release being described. It is expected to point at HEAD:
	// the local path derives the preceding tag by walking back from there, and
	// a release is only ever made from a tagged HEAD.
	Tag string

	// Shallow says the local history is incomplete, so the forge must be
	// asked instead.
	Shallow bool

	// Client and Repo are used when Shallow. Without them a shallow clone
	// falls back to whatever history is present, which is better than an
	// error and worse than the truth.
	Client *github.Client
	Repo   github.Repo
}

// Collect gathers the commits a release should describe, and the tag it
// follows.
//
// Local history is used whenever it is complete, because it needs no network
// and cannot be rate limited. A shallow clone is the one case where the
// answer is not on disk, and that is a normal CI checkout rather than a
// mistake to be corrected with fetch-depth: 0.
// convert adapts forge commits to the shape the rest of the package uses.
func convert(infos []github.CommitInfo) []discover.Commit {
	commits := make([]discover.Commit, 0, len(infos))
	for _, info := range infos {
		subject, body, _ := strings.Cut(info.Commit.Message, "\n")

		// A forge login identifies a contributor better than a git author
		// name, which is whatever was configured on the machine that committed.
		author := info.Commit.Author.Name
		if info.Author != nil && info.Author.Login != "" {
			author = info.Author.Login
		}

		commits = append(commits, discover.Commit{
			SHA:     info.SHA,
			Subject: strings.TrimSpace(subject),
			Body:    strings.TrimSpace(body),
			Author:  author,
		})
	}
	return commits
}

func Collect(ctx context.Context, s Source) (previous string, commits []discover.Commit, err error) {
	if !s.Shallow {
		previous, err = discover.PreviousTag(ctx, s.Dir)
		if err != nil {
			return "", nil, err
		}
		commits, err = discover.Commits(ctx, s.Dir, previous, s.Tag)
		return previous, commits, err
	}

	if s.Client == nil {
		// Say what was lost. Silently producing a one-commit changelog would
		// look like a release that changed almost nothing.
		commits, err = discover.Commits(ctx, s.Dir, "", s.Tag)
		return "", commits, err
	}

	tags, err := s.Client.Tags(ctx, s.Repo, 100)
	if err != nil {
		return "", nil, fmt.Errorf("changelog: listing tags: %w", err)
	}

	previous = semver.Latest(tags, s.Tag)

	// A first release has nothing to compare against, and the compare endpoint
	// requires a base. Reporting no changes would be wrong in the way that is
	// hardest to notice: the local path answers the same question with the
	// whole history, so the two would silently disagree.
	if previous == "" {
		infos, err := s.Client.CommitsUpTo(ctx, s.Repo, s.Tag)
		if err != nil {
			return "", nil, fmt.Errorf("changelog: listing commits up to %s: %w", s.Tag, err)
		}
		// Already newest first, as git log reports them.
		return "", convert(infos), nil
	}

	infos, err := s.Client.Compare(ctx, s.Repo, previous, s.Tag)
	if err != nil {
		return "", nil, fmt.Errorf("changelog: comparing %s...%s: %w", previous, s.Tag, err)
	}

	commits = convert(infos)

	// The compare endpoint returns oldest first; git log and therefore the
	// rest of this package expect newest first.
	slices.Reverse(commits)
	return previous, commits, nil
}
