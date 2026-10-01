package yank

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/danielriddell21/letsgo/internal/brew"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/plugin"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/release"
	"github.com/danielriddell21/letsgo/internal/releases"
	"github.com/danielriddell21/letsgo/internal/semver"
	"github.com/danielriddell21/letsgo/manifest"
)

// Forge is the part of a release API that retracting needs.
type Forge interface {
	ReleaseByTag(ctx context.Context, repo github.Repo, tag string) (*github.Release, error)
	UpdateRelease(ctx context.Context, repo github.Repo, id int64, in github.ReleaseInput) (*github.Release, error)
}

// Options describe a retraction.
type Options struct {
	Client Forge
	Repo   github.Repo

	// Tag is the release being retracted, and Reason is what `go list -m
	// -retracted` will show to anyone about to depend on it.
	Tag    string
	Reason string

	// GoMod is the path to the module's go.mod.
	GoMod string

	// ReadGoMod and WriteGoMod are how go.mod is read and written. Nil is the
	// file on disk. A plan sets them so that the edit is decided by this code
	// and seen, without being made.
	ReadGoMod  func(path string) ([]byte, error)
	WriteGoMod func(path string, data []byte) error

	// Prefix is the module's scope prefix (see discover.Scope), empty for a
	// root module. Tag carries it; go.mod's own retract directive does not,
	// so it has to be stripped before the directive is written and reattached
	// wherever a tag is proposed back to the caller.
	Prefix string

	// Tap and TapAPI revert a Homebrew formula to the previous release. Both
	// are needed; either being absent means no tap to revert.
	Tap    github.Repo
	TapAPI brew.FileAPI

	// Previous is the release to roll the formula back to. Empty leaves the
	// formula alone, which is right when the yanked release was the first.
	Previous string

	// TapFilesPlugin is the tap-files plugin this repository pins, zero if
	// none is. A cask left pointing at a retracted release is a bug nobody
	// sees — it still resolves, just to bytes that say not to use them — so
	// yank re-runs the plugin against the same previous manifest the formula
	// is rolled back from, and PluginRoot is where it runs from.
	TapFilesPlugin plugin.Plugin
	PluginRoot     string
	PluginsDir     string

	// Manifests loads a release's manifest, for regenerating the formula.
	Manifests func(ctx context.Context, tag string) (*manifest.Manifest, error)

	// Project names the module, for a formula whose archives predate the
	// manifest recording binary names.
	Project string

	// Caveats is the formula's caveats block, read from the config. It is not
	// in the manifest — it describes the program rather than the artifacts —
	// so a rollback that did not carry it would quietly drop it from the tap.
	Caveats string

	// RepoInfo is the repository's description, licence and homepage, which
	// the release wrote into the formula. Like Caveats, the manifest does not
	// hold it, and a rollback that left it out would strip it from the tap.
	// Nil leaves the formula without them.
	RepoInfo *github.RepoInfo

	Logf func(format string, args ...any)
}

// Result records what a retraction did.
type Result struct {
	Release   *github.Release
	Retracted bool
	Formulas  []string

	// TapFiles are the paths a tap-files plugin rewrote, rolled back to the
	// previous release alongside the formula.
	TapFiles []string

	// Next is the version the retraction has to be published in before it
	// takes effect. This is the step people miss.
	Next string
}

// IsRetracted reports whether a release's description already carries the
// retraction notice, by checking for its marker.
//
// Exported so that `promote` can refuse to promote a yanked RC using the
// same test yank itself uses to avoid stacking a second notice: the two
// tools must agree on what "retracted" means, and a second copy of this
// check would be a second chance for them to disagree.
func IsRetracted(body string) bool { return releases.IsRetracted(body) }

// notice is prepended to the retracted release's description.
const notice = "> [!CAUTION]\n" +
	"> **This release is retracted.** %s\n" +
	">\n" +
	"> `go get` will not select it, and `go list -m -retracted` explains why.\n" +
	"> Use %s or later.\n\n"

// Run retracts a release.
//
// Four things happen, and only the first two are about Go: the release is
// marked so a human reading the release page sees it, go.mod gains the
// directive so the toolchain sees it, the tap stops pointing at it — the
// formula, and a tap-files plugin's cask if one is pinned — and the caller is
// told the version the retraction must itself be published in.
func Run(ctx context.Context, o Options) (*Result, error) {
	logf := o.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if o.Tag == "" {
		return nil, fmt.Errorf("yank: no tag given")
	}

	result := &Result{Next: NextAfter(o.Tag, o.Prefix)}

	release, err := o.Client.ReleaseByTag(ctx, o.Repo, o.Tag)
	if err != nil {
		return nil, err
	}
	if release == nil {
		return nil, fmt.Errorf("yank: %s has no release tagged %s", o.Repo, o.Tag)
	}

	if err := o.markRelease(ctx, result, release, logf); err != nil {
		return nil, err
	}
	if err := o.editGoMod(result, logf); err != nil {
		return nil, err
	}
	if err := o.revertTap(ctx, result, logf); err != nil {
		return nil, err
	}
	return result, nil
}

// markRelease flags the release as a prerelease and says so at the top of its
// description.
//
// Prerelease rather than deleted: deleting it would break every checksum
// anybody recorded, and the proxy has the module regardless. The point is that
// someone who lands on the release page is told.
func (o Options) markRelease(ctx context.Context, result *Result, release *github.Release, logf func(string, ...any)) error {
	body := release.Body
	if IsRetracted(body) && release.Prerelease {
		result.Release = release
		logf("%s is already marked as retracted", o.Tag)
		return nil
	}
	if !IsRetracted(body) {
		reason := strings.TrimSpace(o.Reason)
		if reason == "" {
			reason = "It should not be used."
		}
		body = fmt.Sprintf(notice, reason, result.Next) + body
	}

	updated, err := o.Client.UpdateRelease(ctx, o.Repo, release.ID, github.ReleaseInput{
		TagName:    release.TagName,
		Name:       release.Name,
		Body:       body,
		Draft:      release.Draft,
		Prerelease: true,
	})
	if err != nil {
		return err
	}
	result.Release = updated
	logf("marked %s as a prerelease and added a retraction notice", o.Tag)
	return nil
}

func (o Options) editGoMod(result *Result, logf func(string, ...any)) error {
	if o.GoMod == "" {
		return nil
	}

	read, write := o.ReadGoMod, o.WriteGoMod
	if read == nil {
		read = os.ReadFile
	}
	if write == nil {
		write = func(path string, data []byte) error { return os.WriteFile(path, data, 0o600) }
	}

	data, err := read(o.GoMod)
	if err != nil {
		return fmt.Errorf("yank: reading %s: %w", o.GoMod, err)
	}

	// go.mod's own retract directive names a version, never a scoped tag: the
	// file already lives in the scope the prefix names.
	version := strings.TrimPrefix(o.Tag, o.Prefix)
	out, changed, err := Retract(data, version, o.Reason)
	if err != nil {
		return err
	}
	if !changed {
		logf("%s already retracts %s", o.GoMod, o.Tag)
		return nil
	}

	if err := write(o.GoMod, out); err != nil {
		return fmt.Errorf("yank: writing %s: %w", o.GoMod, err)
	}
	result.Retracted = true
	logf("added the retract directive to %s", o.GoMod)
	return nil
}

// revertTap points the tap back at the previous release: the formula, and a
// tap-files plugin's cask if one is pinned.
//
// Both are regenerated from that release's own manifest rather than from
// anything kept locally: the digests have to be the ones that release
// published, and they are recorded exactly once, in its letsgo.json.
func (o Options) revertTap(ctx context.Context, result *Result, logf func(string, ...any)) error {
	if o.Tap == (github.Repo{}) || o.TapAPI == nil || o.Manifests == nil {
		return nil
	}
	if o.Previous == "" {
		logf("no earlier release to roll the tap back to; it still points at %s", o.Tag)
		return nil
	}

	m, err := o.Manifests(ctx, o.Previous)
	if err != nil {
		return err
	}

	for _, formula := range brew.FormulasFor(m, o.Repo, o.RepoInfo, o.Project, o.Caveats) {
		published, err := brew.Publish(ctx, o.TapAPI, o.Tap, formula)
		if err != nil {
			return err
		}
		logf("%s %s in %s (back to %s)", published.Status, published.Path, o.Tap, o.Previous)
		result.Formulas = append(result.Formulas, published.Path)
	}

	if err := o.revertNext(ctx, m, logf); err != nil {
		return err
	}

	if o.TapFilesPlugin.Command == "" {
		return nil
	}
	in := release.TapFilesInputFromManifest(m, o.Repo, o.Tap, o.Caveats)
	files, err := release.RunTapFiles(ctx, o.TapFilesPlugin, o.PluginRoot, o.PluginsDir, in)
	if err != nil {
		return err
	}
	for _, f := range files {
		published, err := brew.PublishFile(ctx, o.TapAPI, o.Tap, f.Path, []byte(f.Content),
			fmt.Sprintf("%s %s", o.Project, m.Version))
		if err != nil {
			return err
		}
		logf("%s %s in %s (back to %s)", published.Status, published.Path, o.Tap, o.Previous)
		result.TapFiles = append(result.TapFiles, published.Path)
	}
	return nil
}

// revertNext points each @next formula back at the previous release, when it
// names the one being retracted. One that has moved on to something newer is
// not this release's to undo.
func (o Options) revertNext(ctx context.Context, m *manifest.Manifest, logf func(string, ...any)) error {
	yanked := strings.TrimPrefix(strings.TrimPrefix(o.Tag, o.Prefix), "v")
	for _, formula := range brew.FormulasFor(m, o.Repo, o.RepoInfo, o.Project, o.Caveats) {
		formula.Name = brew.NextName(formula.Name)
		published, err := brew.RevertNext(ctx, o.TapAPI, o.Tap, formula, yanked)
		if err != nil {
			return err
		}
		if published.Status == brew.Kept {
			continue
		}
		logf("%s %s in %s (back to %s)", published.Status, published.Path, o.Tap, o.Previous)
	}
	return nil
}

// NextAfter returns the tag a retraction of tag must be published as, keeping
// tag's own scope prefix.
//
// A patch bump, and never the retracted version itself: the directive lives in
// go.mod, and go.mod is only visible to the toolchain through a version that
// carries it. Retracting v1.2.3 and stopping there leaves the retraction in a
// release nobody will fetch.
func NextAfter(tag, prefix string) string {
	scope := discover.Scope{Prefix: prefix}
	rest, ok := scope.MatchesTag(tag)
	if !ok {
		return ""
	}
	v, ok := semver.Parse(rest)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%sv%d.%d.%d", prefix, v.Major, v.Minor, v.Patch+1)
}

// PreviousOf finds the release before tag in tag's own scope, by version
// rather than by the order the forge lists them in, and ignoring any tag
// outside that scope — a nested module's tags parse as valid versions too.
func PreviousOf(tags []string, tag, prefix string) string {
	scope := discover.Scope{Prefix: prefix}

	targetRest, ok := scope.MatchesTag(tag)
	if !ok {
		return ""
	}
	target, ok := semver.Parse(targetRest)
	if !ok {
		return ""
	}

	best, found := semver.Version{}, ""
	for _, candidate := range tags {
		rest, ok := scope.MatchesTag(candidate)
		if !ok {
			continue
		}
		v, ok := semver.Parse(rest)
		if !ok || semver.Compare(v, target) >= 0 {
			continue
		}
		// A prerelease is not what a retracted release should send people to.
		if v.IsPrerelease() {
			continue
		}
		if found == "" || semver.Compare(v, best) > 0 {
			best, found = v, candidate
		}
	}
	return found
}
