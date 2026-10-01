package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/danielriddell21/letsgo/internal/diff"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/publication"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/yank"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

// yankArgs are the flags `letsgo yank` and `letsgo plan -yank` share.
type yankArgs struct {
	reason  string
	keepTap bool
}

func (y *yankArgs) bind(fs *flag.FlagSet) {
	fs.StringVar(&y.reason, "reason", "", "why the release should not be used; shown by `go list -m -retracted`")
	fs.BoolVar(&y.keepTap, "keep-tap", false, "leave the Homebrew formula pointing at the retracted release")
}

// yankTarget is what a retraction is asked to do.
type yankTarget struct {
	Tag, Reason, Previous string
	KeepTap               bool
}

// yankOptions is the one place a retraction is put together, so that doing it,
// planning it and applying a plan of it all start from the same options.
func yankOptions(m moduleRepo, t yankTarget, tokens diffTokens) yank.Options {
	o := yank.Options{
		Client:   m.Client,
		Repo:     m.Repo,
		Tag:      t.Tag,
		Reason:   t.Reason,
		GoMod:    filepath.Join(m.Module.Dir, "go.mod"),
		Prefix:   m.Scope.Prefix,
		Project:  m.Module.Name,
		Caveats:  brewCaveats(m.Module.Dir),
		Previous: t.Previous,
		Manifests: func(ctx context.Context, tag string) (*manifest.Manifest, error) {
			return diff.Fetch(ctx, m.Client, m.Repo, discover.Scope{}, tag)
		},
	}
	if !t.KeepTap {
		o.Tap, o.TapAPI, o.TapFilesPlugin, o.PluginRoot = tapFor(m.Module.Dir, tapClientFor(m.Client, tokens.TapToken, tokens.Token))
	}
	return o
}

// planYankCommand is `letsgo plan -yank`.
func planYankCommand(tag string, y yankArgs, jsonOutput bool, tokens diffTokens, r diffRun) error {
	if jsonOutput {
		return errors.New("letsgo: a yank plan has no JSON form yet")
	}
	return planYank(context.Background(), tag, y, tokens, r)
}

// planYank says what retracting a release would change and, with out, saves it
// for `letsgo apply`.
func planYank(ctx context.Context, tag string, y yankArgs, tokens diffTokens, r diffRun) error {
	m, err := resolveModuleRepo(ctx, tokens.Token)
	if err != nil {
		return err
	}
	previous, err := previousRelease(ctx, m.Client, m.Repo, tag, m.Scope.Prefix)
	if err != nil {
		return err
	}
	options := yankOptions(m, yankTarget{Tag: tag, Reason: y.reason, Previous: previous, KeepTap: y.keepTap}, tokens)

	fmt.Printf("retract %s from %s\n", tag, m.Repo)
	actions, err := observeYank(ctx, options)
	if err != nil {
		return err
	}

	file := &plandiff.File{
		Schema:        plandiff.FileSchema,
		LetsgoVersion: version,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		Kind:          plandiff.FileKindYank,
		Repo:          m.Repo.Owner + "/" + m.Repo.Name,
		Tag:           tag,
		Reason:        y.reason,
		Previous:      previous,
		Actions:       actions,
	}
	r.Then = "letsgo yank " + tag
	r.Title = "letsgo plan: yank " + tag
	return finishPlan(actions, func(path string) (string, error) { return writePlan(file, path) }, r)
}

// applyYank retracts a release as a saved plan agreed: the forge and go.mod
// must still be as the plan found them, and only what it lists is written.
func applyYank(ctx context.Context, file *plandiff.File, tokens diffTokens) error {
	m, err := resolveModuleRepo(ctx, tokens.Token)
	if err != nil {
		return err
	}
	if repo := m.Repo.Owner + "/" + m.Repo.Name; repo != file.Repo {
		return fmt.Errorf("letsgo: the plan is for %s, and this checkout is %s", file.Repo, repo)
	}

	// A plan that touches no tap file was made without one.
	keepTap := !touches(file.Actions, plandiff.KindTap)
	options := yankOptions(m, yankTarget{Tag: file.Tag, Reason: file.Reason, Previous: file.Previous, KeepTap: keepTap}, tokens)

	current, err := observeYank(ctx, options)
	if err != nil {
		return err
	}
	if err := yankStaleness(file.Actions, current, "letsgo plan -yank "+file.Tag+" -out"); err != nil {
		return err
	}
	for _, name := range alreadyDone(file.Actions, current) {
		fmt.Printf("  = %s is already as planned; skipped\n", name)
	}

	fmt.Println()
	options.Logf = func(format string, args ...any) { fmt.Printf("  "+format+"\n", args...) }
	result, err := yank.Run(ctx, guardYank(options, newPlannedWrites(file.Actions)))
	if err != nil {
		return err
	}
	reportNextSteps(result, file.Tag)
	return nil
}

func touches(actions []plandiff.Action, kind plandiff.Kind) bool {
	for _, a := range actions {
		if a.Kind == kind {
			return true
		}
	}
	return false
}

// yankStaleness is staleness plus the check a release gets from rebuilding: a
// target still to be changed must be changed to what the plan said. A yank has
// no rebuild, so without this a release body or go.mod that came out
// differently would be written without anyone having agreed it.
func yankStaleness(saved, current []plandiff.Action, remake string) error {
	if err := staleness(saved, current, remake); err != nil {
		return err
	}
	now := make(map[string]plandiff.Action, len(current))
	for _, a := range current {
		now[string(a.Kind)+"\x00"+a.Target] = a
	}
	for _, a := range saved {
		c, ok := now[string(a.Kind)+"\x00"+a.Target]
		if a.Op == plandiff.Keep || !ok || c.Op == plandiff.Keep || c.Planned == a.Planned {
			continue
		}
		return fmt.Errorf("letsgo: the plan would now write %s %s as %s, not %s; nothing was changed\n  make a new plan with `%s`",
			a.Kind, a.Target, c.Planned, a.Planned, remake)
	}
	return nil
}

// guardYank holds a retraction to its plan: a write the plan did not list is
// refused, not performed.
func guardYank(o yank.Options, writes *plannedWrites) yank.Options {
	o.Client = guardedRelease{Forge: o.Client, tag: o.Tag, writes: writes}
	if o.TapAPI != nil {
		o.TapAPI = guardedTap{FileAPI: o.TapAPI, writes: writes}
	}
	target := o.Prefix + "go.mod"
	o.WriteGoMod = func(path string, data []byte) error {
		if err := writes.allows(plandiff.KindGoMod, target); err != nil {
			return err
		}
		return os.WriteFile(path, data, 0o600)
	}
	return o
}

// guardedRelease is a yank.Forge that refuses to edit a release the plan did
// not list.
type guardedRelease struct {
	yank.Forge
	tag    string
	writes *plannedWrites
}

func (g guardedRelease) UpdateRelease(ctx context.Context, repo github.Repo, id int64, in github.ReleaseInput) (*github.Release, error) {
	if err := g.writes.allows(plandiff.KindRelease, g.tag); err != nil {
		return nil, err
	}
	return g.Forge.UpdateRelease(ctx, repo, id, in)
}

// observeYank says what retracting would change, by running the retraction
// against a view that can be read but not written to.
func observeYank(ctx context.Context, o yank.Options) ([]plandiff.Action, error) {
	release := &releaseObserver{api: o.Client}
	gomod := &goModObserver{}
	var tap *publication.TapObserver

	o.Client = release
	o.ReadGoMod, o.WriteGoMod = gomod.read, gomod.write
	o.Logf = nil
	if o.TapAPI != nil {
		tap = publication.NewTapObserver(o.TapAPI)
		o.TapAPI = tap
	}
	if _, err := yank.Run(ctx, o); err != nil {
		return nil, err
	}

	actions := []plandiff.Action{release.action(o.Tag)}
	if o.GoMod != "" {
		actions = append(actions, gomod.action(o.Prefix+"go.mod"))
	}
	if tap != nil {
		actions = append(actions, tap.Actions()...)
	}
	return actions, nil
}

// releaseObserver reads a release and notes whether it would be edited.
type releaseObserver struct {
	api    yank.Forge
	edited bool
}

func (r *releaseObserver) ReleaseByTag(ctx context.Context, repo github.Repo, tag string) (*github.Release, error) {
	return r.api.ReleaseByTag(ctx, repo, tag)
}

func (r *releaseObserver) UpdateRelease(_ context.Context, _ github.Repo, id int64, in github.ReleaseInput) (*github.Release, error) {
	r.edited = true
	return &github.Release{
		ID: id, TagName: in.TagName, Name: in.Name, Body: in.Body, Draft: in.Draft, Prerelease: in.Prerelease,
	}, nil
}

func (r *releaseObserver) action(tag string) plandiff.Action {
	op := plandiff.Keep
	if r.edited {
		op = plandiff.Change
	}
	return plandiff.Action{Op: op, Kind: plandiff.KindRelease, Target: tag}
}

// goModObserver reads go.mod from disk and notes what would be written to it.
type goModObserver struct {
	observed, planned string
	written           bool
}

func (g *goModObserver) read(path string) ([]byte, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the module's own go.mod
	if err != nil {
		return nil, err //nolint:wrapcheck // yank names the file
	}
	g.observed = publication.BlobFingerprint(data)
	g.planned = g.observed
	return data, nil
}

func (g *goModObserver) write(_ string, data []byte) error {
	if g.observed == "" {
		return errors.New("letsgo: go.mod was written without being read first")
	}
	g.planned, g.written = publication.BlobFingerprint(data), true
	return nil
}

func (g *goModObserver) action(target string) plandiff.Action {
	op := plandiff.Keep
	if g.written {
		op = plandiff.Change
	}
	return plandiff.Action{Op: op, Kind: plandiff.KindGoMod, Target: target, Observed: g.observed, Planned: g.planned}
}
