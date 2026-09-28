package promote

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/danielriddell21/letsgo/internal/changelog"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/semver"
)

// prereleaseHistory rebuilds the "Prerelease history" section from the RC
// tags themselves — every prerelease sharing the stable tag's exact version
// — rather than copying their old release descriptions, so a hand edit to an
// RC's notes is never carried into the release that supersedes it (PR-13).
//
// Each entry's own "since" boundary is scope.PreviousTag, the same
// release-kind rule (see semver.Previous) that decided the RC's own notes
// when it was first published — so this reconstructs what those notes said
// rather than inventing a second rule for the same question.
func prereleaseHistory(ctx context.Context, o Options, stableTag string) (string, error) {
	if o.Shallow {
		// A shallow clone cannot walk each RC's own local history. The
		// cumulative notes already cover everything since the last stable
		// release via the forge, so the collapsed section is left out rather
		// than built from an incomplete picture.
		return "", nil
	}

	tags, err := discover.Tags(ctx, o.Dir, o.Prefix)
	if err != nil {
		return "", err
	}
	scope := discover.Scope{Prefix: o.Prefix}

	target, ok := semver.Parse(strings.TrimPrefix(stableTag, o.Prefix))
	if !ok {
		return "", fmt.Errorf("promote: %s does not parse as a version", stableTag)
	}

	rcTags := prereleasesOf(tags, scope, target)
	if len(rcTags) == 0 {
		return "", nil
	}

	nested, err := discover.NestedModuleDirs(o.Dir)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	for _, tag := range rcTags {
		previous, _ := scope.PreviousTag(tags, tag)
		commits, err := discover.Commits(ctx, o.Dir, previous, tag, nested...)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "#### %s\n\n%s\n", tag, changelog.Build(previous, tag, commits).Markdown())
	}
	return b.String(), nil
}

// prereleasesOf returns every tag in scope that is a prerelease of exactly
// target's Major.Minor.Patch, newest first.
func prereleasesOf(tags []string, scope discover.Scope, target semver.Version) []string {
	var rcTags []string
	for _, tag := range tags {
		rest, ok := scope.MatchesTag(tag)
		if !ok {
			continue
		}
		v, ok := semver.Parse(rest)
		if !ok || !v.IsPrerelease() {
			continue
		}
		if v.Major != target.Major || v.Minor != target.Minor || v.Patch != target.Patch {
			continue
		}
		rcTags = append(rcTags, tag)
	}

	sort.Slice(rcTags, func(i, j int) bool {
		vi, _ := scope.MatchesTag(rcTags[i])
		vj, _ := scope.MatchesTag(rcTags[j])
		a, _ := semver.Parse(vi)
		b, _ := semver.Parse(vj)
		return semver.Compare(a, b) > 0
	})
	return rcTags
}
