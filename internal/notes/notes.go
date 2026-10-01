// Package notes writes a release's notes: the changelog since the previous
// tag, the "what shipped" comparison of manifests, and the manifest's
// fingerprint. `letsgo release` and `letsgo promote` both build them here, so
// either reads the same.
package notes

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"

	"github.com/danielriddell21/letsgo/internal/changelog"
	"github.com/danielriddell21/letsgo/internal/diff"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/feature"
	"github.com/danielriddell21/letsgo/internal/pgpwords"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/randomart"
	"github.com/danielriddell21/letsgo/manifest"
)

// Source is everything the notes are made from.
type Source struct {
	Plan   *plan.Plan
	Client *github.Client
	Repo   github.Repo
	// Manifest is the release being written up; ManifestSum is its digest,
	// empty when there is none to fingerprint.
	Manifest    *manifest.Manifest
	ManifestSum []byte
	// Logf reports what was skipped or fetched. Nil discards it.
	Logf func(format string, args ...any)
}

func (s Source) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

// Release builds the changelog for everything since the previous tag, then
// the sections Extra adds.
//
// A shallow checkout is the normal shape of a CI clone, so the history is
// fetched from the forge rather than demanded of the caller.
func Release(ctx context.Context, s Source) (string, error) {
	p := s.Plan
	if !p.Features.On(feature.Changelog) {
		return "", nil
	}
	if p.Git.Shallow {
		s.logf("shallow clone; reading history from the forge")
	}

	previous, commits, err := changelog.Collect(ctx, changelog.Source{
		Dir:     p.Module.Dir,
		GitBin:  p.GitBin,
		Tag:     p.Tag,
		Prefix:  p.Scope.Prefix,
		Shallow: p.Git.Shallow,
		Client:  s.Client,
		Repo:    s.Repo,
	})
	if err != nil {
		return "", err
	}
	notes := changelog.Build(previous, p.Tag, commits).WithAPIChanges(p.APIChanges).Markdown()
	extra, err := Extra(ctx, s, previous)
	if err != nil {
		return "", err
	}
	return notes + extra, nil
}

// Extra is the sections that follow the changelog: what shipped against the
// previous release, then the manifest's fingerprint.
func Extra(ctx context.Context, s Source, previous string) (string, error) {
	p := s.Plan
	var notes string
	if p.Features.On(feature.DiffNotes) {
		shipped, err := s.whatShipped(ctx, previous)
		if err != nil {
			return "", err
		}
		notes += shipped
	}
	if p.Features.On(feature.Randomart) {
		switch {
		case len(s.ManifestSum) > 0:
			notes += fingerprint(p.Tag, s.ManifestSum)
		case slices.Contains(p.Required, "randomart"):
			return "", errors.New("randomart is required, but there is no manifest digest to fingerprint")
		}
	}
	return notes, nil
}

// fingerprint renders the collapsed block that identifies a release by its
// manifest. It is release notes only: the manifest cannot contain it, since
// the manifest is what it is made from.
func fingerprint(tag string, sum []byte) string {
	return fmt.Sprintf("\n<details><summary>Manifest fingerprint</summary>\n\n```\n%s\n```\n\n`sha256:%s`\n\n```\n%s```\n\n</details>\n",
		randomart.Render(sum, randomart.Title(tag)), hex.EncodeToString(sum), pgpwords.Rows(pgpwords.Encode(sum)))
}

// whatShipped renders the manifest-diff section against the same previous
// release the changelog just used. A release must not fail merely because
// this supplementary section couldn't be built — a first release has no
// previous tag, and a release published before letsgo recorded a manifest has
// nothing to fetch — so any failure here is reported and the section is left
// out, rather than propagated. A release that required diff-notes asked for
// exactly that failure, so it is returned instead.
func (s Source) whatShipped(ctx context.Context, previous string) (string, error) {
	strict := slices.Contains(s.Plan.Required, "diff-notes")
	if previous == "" {
		if strict {
			return "", errors.New("diff-notes is required, but there is no previous release to compare")
		}
		return "", nil
	}
	before, err := diff.Fetch(ctx, s.Client, s.Repo, discover.Scope{}, previous)
	if err != nil {
		if strict {
			return "", fmt.Errorf("diff-notes is required, but %s has no manifest to compare: %w", previous, err)
		}
		s.logf("! skipped the \"what shipped\" section: %v", err)
		return "", nil
	}
	return diff.Compare(before, s.Manifest).Notes(previous), nil
}
