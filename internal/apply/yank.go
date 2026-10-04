package apply

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/danielriddell21/letsgo/internal/publication"
	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/internal/yank"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

// Yank retracts a release as a saved plan agreed: the forge and go.mod must
// still be as the plan found them, and only what the plan lists is written.
// options are the retraction as it would be made now, and logf where progress
// goes.
func Yank(ctx context.Context, file *plandiff.File, options yank.Options, logf func(string, ...any)) (*yank.Result, error) {
	current, err := ObserveYank(ctx, options)
	if err != nil {
		return nil, err
	}
	if err := YankStale(file.Actions, current, "letsgo plan -yank "+file.Tag+" -out"); err != nil {
		return nil, err
	}
	for _, name := range AlreadyDone(file.Actions, current) {
		logf("  = %s is already as planned; skipped", name)
	}

	options.Logf = func(format string, args ...any) { logf("  "+format, args...) }
	return yank.Run(ctx, NewWrites(file.Actions).Yank(options))
}

// Touches reports whether the plan lists an action of kind.
func Touches(actions []plandiff.Action, kind plandiff.Kind) bool {
	for _, a := range actions {
		if a.Kind == kind {
			return true
		}
	}
	return false
}

// YankStale is Stale plus the check a release gets from rebuilding: a
// target still to be changed must be changed to what the plan said. A yank has
// no rebuild, so without this a release body or go.mod that came out
// differently would be written without anyone having agreed it.
func YankStale(saved, current []plandiff.Action, remake string) error {
	if err := Stale(saved, current, remake); err != nil {
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

// ObserveYank says what retracting would change, by running the retraction
// against a view that can be read but not written to.
func ObserveYank(ctx context.Context, o yank.Options) ([]plandiff.Action, error) {
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
