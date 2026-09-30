package main

import (
	"context"
	"fmt"
	"io"

	"github.com/danielriddell21/letsgo/internal/brew"
	"github.com/danielriddell21/letsgo/internal/publish"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

// plannedWrites lets an apply write only what its plan said it would.
//
// The publisher makes its own decisions, and they agree with the plan because
// the forge was checked against it first. This is the second lock on the same
// door: a write the plan did not list is refused, not performed.
type plannedWrites struct {
	pending map[plandiff.Kind]map[string]plandiff.Op
	listed  map[plandiff.Kind]map[string]bool
}

func newPlannedWrites(actions []plandiff.Action) *plannedWrites {
	w := &plannedWrites{pending: plandiff.Pending(actions), listed: map[plandiff.Kind]map[string]bool{}}
	for _, a := range actions {
		if w.listed[a.Kind] == nil {
			w.listed[a.Kind] = map[string]bool{}
		}
		w.listed[a.Kind][a.Target] = true
	}
	return w
}

func (w *plannedWrites) allows(kind plandiff.Kind, target string) error {
	if _, ok := w.pending[kind][target]; ok {
		return nil
	}
	return fmt.Errorf("letsgo: refused to write %s %s: it is not in the plan", kind, target)
}

// anyAsset reports whether the plan changes or adds an asset at all.
func (w *plannedWrites) anyAsset() bool { return len(w.pending[plandiff.KindAsset]) > 0 }

// guardedForge is a publish.Forge that refuses a write the plan did not list.
type guardedForge struct {
	publish.Forge
	tag    string
	writes *plannedWrites
}

func (g guardedForge) CreateRelease(ctx context.Context, repo github.Repo, in github.ReleaseInput) (*github.Release, error) {
	if err := g.writes.allows(plandiff.KindRelease, g.tag); err != nil {
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
	if err := g.writes.allows(plandiff.KindAsset, name); err != nil {
		return nil, err
	}
	return g.Forge.UploadAsset(ctx, repo, releaseID, name, size, content)
}

// guardedTap is a brew.FileAPI that refuses a write the plan did not list.
type guardedTap struct {
	brew.FileAPI
	writes *plannedWrites
}

func (g guardedTap) WriteFile(ctx context.Context, repo github.Repo, in github.FileInput) error {
	if err := g.writes.allows(plandiff.KindTap, in.Path); err != nil {
		return err
	}
	return g.FileAPI.WriteFile(ctx, repo, in)
}
