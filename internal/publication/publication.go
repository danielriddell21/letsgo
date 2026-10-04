// Package publication makes a built release available: the forge release and
// its assets, the checksum and proxy gate that stands in front of them, the
// Homebrew tap and the container images.
//
// It starts from a release that is already built and does not include the
// build. Everything it writes goes through the forge and tap it is handed, so
// a caller can substitute a rehearsal or a guarded view of either.
package publication

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/danielriddell21/letsgo/internal/brew"
	"github.com/danielriddell21/letsgo/internal/feature"
	"github.com/danielriddell21/letsgo/internal/forgerelease"
	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/release"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

// Options is everything a publication depends on beyond the plan it follows.
type Options struct {
	Plan   *plan.Plan
	Result *release.Result

	// Dir is where the release was built, which holds the files to upload.
	Dir  string
	Repo github.Repo

	// Forge and Tap are where the release and the formula are written.
	Forge forgerelease.Forge
	Tap   brew.FileAPI

	// Info is what the repository says about itself, for the formula.
	Info *github.RepoInfo

	// Notes is the release description.
	Notes string

	// Append adds Notes after an existing description instead of replacing it.
	Append bool

	// Token is the credential the container registry is written with.
	Token string

	// Snapshot rehearses: nothing public is consulted, and the images are
	// described rather than pushed.
	Snapshot bool

	// Out receives what the publication reports. Nil discards it.
	Out io.Writer
}

// Result is what a publication did.
type Result struct {
	Forge *forgerelease.Result
}

// Tag is the tag the release is published under.
func Tag(p *plan.Plan) string {
	if p.Tag != "" {
		return p.Tag
	}
	return "v" + p.Version
}

// Publish makes the release available, in the order that keeps a failure
// harmless: the gate first, so that a disagreement with the checksum database
// stops a release that is not yet public; the forge release next; the tap and
// the images last, because a formula names download URLs that exist only once
// the assets are attached.
//
// A draft holds back the tap and the images, which would otherwise point the
// world at assets only the author can see.
func Publish(ctx context.Context, o Options) (*Result, error) {
	out := o.out()
	var done []Step

	if err := gate(ctx, out, o); err != nil {
		return nil, &StepError{Step: StepGate, Done: done, Err: err}
	}
	done = append(done, StepGate)

	ro, err := releaseOptions(o, func(format string, args ...any) {
		fmt.Fprintf(out, "  "+format+"\n", args...)
	})
	if err != nil {
		return nil, &StepError{Step: StepRelease, Done: done, Err: err}
	}
	released, err := forgerelease.Run(ctx, ro)
	if err != nil {
		return nil, &StepError{Step: StepRelease, Done: done, Err: err}
	}
	done = append(done, StepRelease)
	reportPublished(out, released)

	if err := publishTap(ctx, out, o.Plan, o.Result, o.Tap, o.Repo, o.Info); err != nil {
		return nil, &StepError{Step: StepTap, Done: done, Err: err}
	}
	done = append(done, StepTap)

	if err := publishImages(ctx, out, o); err != nil {
		return nil, &StepError{Step: StepImages, Done: done, Err: err}
	}
	return &Result{Forge: released}, nil
}

// Observe says what Publish would do to the forge, tap and registry, by
// running the same decisions against views that can only be read. The
// prediction cannot drift from the practice because it is the practice.
func Observe(ctx context.Context, o Options) ([]plandiff.Action, error) {
	ro, err := releaseOptions(o, nil)
	if err != nil {
		return nil, fmt.Errorf("observing the release: %w", err)
	}
	actions, err := forgerelease.Observe(ctx, ro)
	if err != nil {
		return nil, fmt.Errorf("observing the release: %w", err)
	}

	if o.Plan.Config.Draft {
		return actions, nil
	}

	tap := NewTapObserver(o.Tap)
	if err := publishTap(ctx, io.Discard, o.Plan, o.Result, tap, o.Repo, o.Info); err != nil {
		return nil, fmt.Errorf("observing the tap: %w", err)
	}
	actions = append(actions, tap.Actions()...)

	images, err := release.ObserveImages(ctx, o.Result.Images, o.Token)
	if err != nil {
		return nil, fmt.Errorf("observing the images: %w", err)
	}
	return append(actions, images...), nil
}

func (o Options) out() io.Writer {
	if o.Out == nil {
		return io.Discard
	}
	return o.Out
}

// releaseOptions is the publication of the forge release, decided once so that
// what Publish does and what Observe predicts cannot drift apart.
func releaseOptions(o Options, logf func(format string, args ...any)) (forgerelease.Options, error) {
	p := o.Plan
	sums, err := sumsFrom(o.Result)
	if err != nil {
		return forgerelease.Options{}, err
	}
	return forgerelease.Options{
		Client: o.Forge,
		Repo:   o.Repo,
		Dir:    o.Dir,
		Files:  o.Result.Files,
		Sums:   sums,
		Notes:  notesMode(o.Append, p.Features.On(feature.Changelog)),
		Release: github.ReleaseInput{
			TagName:         Tag(p),
			Name:            releaseTitle(p),
			Body:            o.Notes,
			Draft:           p.Draft(),
			Prerelease:      isPrerelease(p),
			MakeLatest:      isLatest(p),
			TargetCommitish: p.Git.Commit,
		},
		Logf: logf,
	}, nil
}

// releaseTitle is the release's display name: the tag alone for a root
// module, unchanged from every single-module repository today, or the
// module's own directory plus the version for a scoped one — so two modules
// in the same repository read apart on the releases page instead of both
// showing a bare tag with an unlabeled prefix.
func releaseTitle(p *plan.Plan) string {
	tag := Tag(p)
	if p.Scope.Dir == "" {
		return tag
	}
	return p.Scope.Dir + " " + strings.TrimPrefix(tag, p.Scope.Prefix)
}

// notesMode also treats a disabled changelog as append-with-nothing-generated,
// so resuming a release with `disable changelog` set never blanks an existing
// description the way replacing it with empty notes would.
func notesMode(appendNotes, changelogEnabled bool) forgerelease.NotesMode {
	if appendNotes || !changelogEnabled {
		return forgerelease.NotesAppend
	}
	return forgerelease.NotesReplace
}

// sumsFrom is the digest of every file about to be published, read from disk:
// an upload is only conclusively the file we built if its digest says so.
func sumsFrom(r *release.Result) (map[string]string, error) {
	sums, err := r.Digests()
	if err != nil {
		return nil, fmt.Errorf("publication: %w", err)
	}
	return sums, nil
}

// isPrerelease follows semver: a version carrying a pre-release segment is one.
func isPrerelease(p *plan.Plan) bool {
	switch p.Prerelease() {
	case plan.Yes:
		return true
	case plan.No:
		return false
	}
	base, _, _ := strings.Cut(p.Version, "+")
	return strings.Contains(base, "-")
}

// isLatest reports GitHub's make_latest value: a repository has one "latest"
// release, so auto claims it for a root module and defers for a scoped one
// (see discover.Scope), rather than fight whichever module released last for
// the badge.
func isLatest(p *plan.Plan) string {
	switch p.Latest() {
	case plan.Yes:
		return "true"
	case plan.No:
		return "false"
	}
	if p.Scope.Prefix == "" {
		return "true"
	}
	return "false"
}

// reportPublished summarises what reached the forge.
func reportPublished(out io.Writer, published *forgerelease.Result) {
	if published.NotesRefused {
		fmt.Fprintln(out, "  ! the release description could not be updated with this token")
	}

	fmt.Fprintf(out, "  uploaded %d, skipped %d", len(published.Uploaded), len(published.Skipped))
	if len(published.Replaced) > 0 {
		fmt.Fprintf(out, ", replaced %d", len(published.Replaced))
	}
	fmt.Fprintln(out)
}
