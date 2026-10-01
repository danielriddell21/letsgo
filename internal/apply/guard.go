// Package apply holds a plan's enforcement: what an apply may write, and when a
// plan has gone stale. It is ADR-0019's guarantee, kept apart from the flags
// that reach it.
package apply

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/danielriddell21/letsgo/internal/brew"
	"github.com/danielriddell21/letsgo/internal/publish"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/yank"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

// Writes lets an apply write only what its plan said it would.
//
// The publisher makes its own decisions, and they agree with the plan because
// the forge was checked against it first. This is the second lock on the same
// door: a write the plan did not list is refused, not performed.
// Writes lets an apply write only what its plan said it would.
type Writes struct {
	pending map[plandiff.Kind]map[string]plandiff.Op
	listed  map[plandiff.Kind]map[string]bool
}

// NewWrites holds an apply to actions.
func NewWrites(actions []plandiff.Action) *Writes {
	w := &Writes{pending: plandiff.Pending(actions), listed: map[plandiff.Kind]map[string]bool{}}
	for _, a := range actions {
		if w.listed[a.Kind] == nil {
			w.listed[a.Kind] = map[string]bool{}
		}
		w.listed[a.Kind][a.Target] = true
	}
	return w
}

// Allows refuses a write of kind to target unless the plan changes or adds it.
func (w *Writes) Allows(kind plandiff.Kind, target string) error {
	if _, ok := w.pending[kind][target]; ok {
		return nil
	}
	return fmt.Errorf("letsgo: refused to write %s %s: it is not in the plan", kind, target)
}

// anyAsset reports whether the plan changes or adds an asset at all.
func (w *Writes) anyAsset() bool { return len(w.pending[plandiff.KindAsset]) > 0 }

// Forge wraps f so it refuses a write the plan did not list. tag is the
// release the plan is for.
func (w *Writes) Forge(f publish.Forge, tag string) publish.Forge {
	return guardedForge{Forge: f, tag: tag, writes: w}
}

// Tap wraps t so it refuses a write the plan did not list.
func (w *Writes) Tap(t brew.FileAPI) brew.FileAPI { return guardedTap{FileAPI: t, writes: w} }

// guardedForge is a publish.Forge that refuses a write the plan did not list.
type guardedForge struct {
	publish.Forge
	tag    string
	writes *Writes
}

func (g guardedForge) CreateRelease(ctx context.Context, repo github.Repo, in github.ReleaseInput) (*github.Release, error) {
	if err := g.writes.Allows(plandiff.KindRelease, g.tag); err != nil {
		return nil, err
	}
	return g.Forge.CreateRelease(ctx, repo, in)
}

// UpdateRelease is how a release that exists is resumed, whether or not its
// notes change, so any release action in the plan covers it.
func (g guardedForge) UpdateRelease(ctx context.Context, repo github.Repo, id int64, in github.ReleaseInput) (*github.Release, error) {
	if !g.writes.listed[plandiff.KindRelease][g.tag] {
		return nil, fmt.Errorf("letsgo: refused to update release %s: it is not in the plan", g.tag)
	}
	return g.Forge.UpdateRelease(ctx, repo, id, in)
}

// DeleteAsset is addressed by id, not name, so it is allowed when the plan
// replaces an asset: that is the only reason a release deletes one.
func (g guardedForge) DeleteAsset(ctx context.Context, repo github.Repo, assetID int64) error {
	if !g.writes.anyAsset() {
		return fmt.Errorf("letsgo: refused to delete an asset: the plan does not change any")
	}
	return g.Forge.DeleteAsset(ctx, repo, assetID)
}

func (g guardedForge) UploadAsset(ctx context.Context, repo github.Repo, releaseID int64, name string, size int64, content io.Reader) (*github.Asset, error) {
	if err := g.writes.Allows(plandiff.KindAsset, name); err != nil {
		return nil, err
	}
	return g.Forge.UploadAsset(ctx, repo, releaseID, name, size, content)
}

// guardedTap is a brew.FileAPI that refuses a write the plan did not list.
type guardedTap struct {
	brew.FileAPI
	writes *Writes
}

func (g guardedTap) WriteFile(ctx context.Context, repo github.Repo, in github.FileInput) error {
	if err := g.writes.Allows(plandiff.KindTap, in.Path); err != nil {
		return err
	}
	return g.FileAPI.WriteFile(ctx, repo, in)
}

// Yank holds a retraction to its plan: a write the plan did not list is
// refused, not performed.
func (writes *Writes) Yank(o yank.Options) yank.Options {
	o.Client = guardedRelease{Forge: o.Client, tag: o.Tag, writes: writes}
	if o.TapAPI != nil {
		o.TapAPI = writes.Tap(o.TapAPI)
	}
	target := o.Prefix + "go.mod"
	o.WriteGoMod = func(path string, data []byte) error {
		if err := writes.Allows(plandiff.KindGoMod, target); err != nil {
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
	writes *Writes
}

func (g guardedRelease) UpdateRelease(ctx context.Context, repo github.Repo, id int64, in github.ReleaseInput) (*github.Release, error) {
	if err := g.writes.Allows(plandiff.KindRelease, g.tag); err != nil {
		return nil, err
	}
	return g.Forge.UpdateRelease(ctx, repo, id, in)
}
