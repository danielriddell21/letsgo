package yank

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/danielriddell21/letsgo/internal/brew"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/semver"
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

	// Tap and TapAPI revert a Homebrew formula to the previous release. Both
	// are needed; either being absent means no tap to revert.
	Tap    github.Repo
	TapAPI brew.FileAPI

	// Previous is the release to roll the formula back to. Empty leaves the
	// formula alone, which is right when the yanked release was the first.
	Previous string

	// Manifests loads a release's manifest, for regenerating the formula.
	Manifests func(ctx context.Context, tag string) (*manifest.Manifest, error)

	// Project names the module, for a formula whose archives predate the
	// manifest recording binary names.
	Project string

	Logf func(format string, args ...any)
}

// Result records what a retraction did.
type Result struct {
	Release   *github.Release
	Retracted bool
	Formulas  []string

	// Next is the version the retraction has to be published in before it
	// takes effect. This is the step people miss.
	Next string
}

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
// directive so the toolchain sees it, the formula stops pointing at it, and
// the caller is told the version the retraction must itself be published in.
func Run(ctx context.Context, o Options) (*Result, error) {
	logf := o.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if o.Tag == "" {
		return nil, fmt.Errorf("yank: no tag given")
	}

	result := &Result{Next: NextAfter(o.Tag)}

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
	if err := o.revertFormula(ctx, result, logf); err != nil {
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
	if !strings.HasPrefix(body, "> [!CAUTION]") {
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

	data, err := os.ReadFile(o.GoMod)
	if err != nil {
		return fmt.Errorf("yank: reading %s: %w", o.GoMod, err)
	}

	out, changed, err := Retract(data, o.Tag, o.Reason)
	if err != nil {
		return err
	}
	if !changed {
		logf("%s already retracts %s", o.GoMod, o.Tag)
		return nil
	}

	if err := os.WriteFile(o.GoMod, out, 0o600); err != nil {
		return fmt.Errorf("yank: writing %s: %w", o.GoMod, err)
	}
	result.Retracted = true
	logf("added the retract directive to %s", o.GoMod)
	return nil
}

// revertFormula points the tap back at the previous release.
//
// Regenerated from that release's own manifest rather than from anything kept
// locally: the digests have to be the ones that release published, and they
// are recorded exactly once, in its letsgo.json.
func (o Options) revertFormula(ctx context.Context, result *Result, logf func(string, ...any)) error {
	if o.Tap == (github.Repo{}) || o.TapAPI == nil || o.Manifests == nil {
		return nil
	}
	if o.Previous == "" {
		logf("no earlier release to roll the formula back to; the tap still points at %s", o.Tag)
		return nil
	}

	m, err := o.Manifests(ctx, o.Previous)
	if err != nil {
		return err
	}

	for _, formula := range FormulasFrom(m, o.Repo, o.Project) {
		published, err := brew.Publish(ctx, o.TapAPI, o.Tap, formula)
		if err != nil {
			return err
		}
		logf("%s %s in %s (back to %s)", published.Status, published.Path, o.Tap, o.Previous)
		result.Formulas = append(result.Formulas, published.Path)
	}
	return nil
}

// FormulasFrom rebuilds a release's Homebrew formulas from its manifest.
func FormulasFrom(m *manifest.Manifest, repo github.Repo, project string) []brew.Formula {
	tag := m.Tag
	if tag == "" {
		tag = "v" + m.Version
	}

	var order []string
	platforms := map[string][]brew.Platform{}

	for _, a := range m.Artifacts {
		// Archives published before the manifest recorded binary names carry
		// the project's name, which for a single-command module is the same
		// answer.
		binary := a.Binary
		if binary == "" {
			binary = project
		}
		if _, seen := platforms[binary]; !seen {
			order = append(order, binary)
		}
		platforms[binary] = append(platforms[binary], brew.Platform{
			OS: a.OS, Arch: a.Arch,
			URL:    github.DownloadURL(repo, tag, a.Name),
			SHA256: a.SHA256,
		})
	}
	sort.Strings(order)

	out := make([]brew.Formula, 0, len(order))
	for _, binary := range order {
		out = append(out, brew.Formula{
			Binary:    binary,
			Version:   m.Version,
			Homepage:  fmt.Sprintf("https://github.com/%s/%s", repo.Owner, repo.Name),
			Platforms: platforms[binary],
		})
	}
	return out
}

// NextAfter returns the version a retraction of tag must be published in.
//
// A patch bump, and never the retracted version itself: the directive lives in
// go.mod, and go.mod is only visible to the toolchain through a version that
// carries it. Retracting v1.2.3 and stopping there leaves the retraction in a
// release nobody will fetch.
func NextAfter(tag string) string {
	v, ok := semver.Parse(tag)
	if !ok {
		return ""
	}
	return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch+1)
}

// PreviousOf finds the release before tag, by version rather than by the order
// the forge lists them in.
func PreviousOf(tags []string, tag string) string {
	target, ok := semver.Parse(tag)
	if !ok {
		return ""
	}

	best, found := semver.Version{}, ""
	for _, candidate := range tags {
		v, ok := semver.Parse(candidate)
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
