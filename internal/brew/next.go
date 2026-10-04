package brew

import (
	"context"
	"fmt"
	"regexp"

	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/internal/semver"
)

// Kept means the tap already had something at least as new at that path, so
// nothing was written. Unlike Unchanged, the content here was never compared
// byte for byte: it says the existing formula was deliberately left alone.
const Kept Status = "kept"

// NextName is the "next" formula's name for a project whose stable formula
// is name: whichever of the newest prerelease or the newest stable is
// currently ahead.
func NextName(name string) string { return name + "@next" }

// PublishNext writes f to the tap's @next formula, unless the version
// already published there is newer or equal.
//
// Every release, prerelease and stable alike, is a candidate: a normal
// rc -> stable progression advances it each time, and a stable backport
// releasing behind an already-published prerelease correctly leaves it
// alone. f.Name is taken as given — the caller names it via NextName.
func PublishNext(ctx context.Context, api FileAPI, tap github.Repo, f Formula) (Result, error) {
	content, err := f.Render()
	if err != nil {
		return Result{}, err
	}
	path := f.FileName()

	newer, err := newerThanPublished(ctx, api, tap, path, f.Version)
	if err != nil {
		return Result{}, err
	}
	if !newer {
		return Result{Path: path, Status: Kept}, nil
	}

	return publish(ctx, api, tap, path, content, fmt.Sprintf("%s %s", f.Name, f.Version))
}

// RevertNext points the @next formula back at f, but only while it still names
// version: the release being retracted. A @next that has since moved on to a
// newer release, or that does not exist, is left alone.
func RevertNext(ctx context.Context, api FileAPI, tap github.Repo, f Formula, version string) (Result, error) {
	path := f.FileName()
	existing, err := api.ReadFile(ctx, tap, path)
	if err != nil {
		return Result{}, err
	}
	if existing == nil {
		return Result{Path: path, Status: Kept}, nil
	}
	if current, ok := versionOf(existing.Content); !ok || current != version {
		return Result{Path: path, Status: Kept}, nil
	}

	content, err := f.Render()
	if err != nil {
		return Result{}, err
	}
	return publish(ctx, api, tap, path, content, fmt.Sprintf("%s %s", f.Name, f.Version))
}

// newerThanPublished reports whether version is newer than whatever formula
// is currently at path. A path that doesn't exist yet has nothing for
// version to be older than, so it always counts as newer. A formula that
// exists but whose version can't be read back is left alone rather than
// guessed at: only a version this package itself wrote is ever compared
// against, so anything else stays put.
func newerThanPublished(ctx context.Context, api FileAPI, tap github.Repo, path, version string) (bool, error) {
	existing, err := api.ReadFile(ctx, tap, path)
	if err != nil {
		return false, err
	}
	if existing == nil {
		return true, nil
	}

	releasing, ok := semver.Parse(version)
	if !ok {
		return false, fmt.Errorf("brew: %q is not a version PublishNext can compare", version)
	}

	current, ok := versionOf(existing.Content)
	if !ok {
		return false, nil
	}
	published, ok := semver.Parse(current)
	if !ok {
		return false, nil
	}

	return semver.Compare(releasing, published) > 0, nil
}

// versionLine matches the `version "..."` line Render always writes.
var versionLine = regexp.MustCompile(`(?m)^\s*version\s+"([^"]*)"`)

// versionOf reads the version a rendered formula carries.
func versionOf(content []byte) (string, bool) {
	m := versionLine.FindSubmatch(content)
	if m == nil {
		return "", false
	}
	return string(m[1]), true
}
