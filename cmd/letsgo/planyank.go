package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/danielriddell21/letsgo/internal/apply"
	"github.com/danielriddell21/letsgo/internal/diff"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/internal/releaser"
	"github.com/danielriddell21/letsgo/internal/yank"
	"github.com/danielriddell21/letsgo/manifest"
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
func (f forge) yankOptions(ctx context.Context, m moduleRepo, t yankTarget, tokens diffTokens) yank.Options {
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
		tapFor(&o, m.Module.Dir, f.tapClientFor(ctx, m.Client, tokens.TapToken, tokens.Token))
		// The release wrote its description, licence and homepage into the
		// formula, so the rollback has to read them again or it drops them.
		if o.Tap != (github.Repo{}) {
			o.RepoInfo = releaser.DescribeRepo(ctx, m.Client, m.Repo, os.Stdout)
		}
	}
	return o
}

// planYankCommand is `letsgo plan -yank`.
func (f forge) planYankCommand(tag string, y yankArgs, jsonOutput bool, tokens diffTokens, r diffRun) error {
	if jsonOutput {
		return errors.New("letsgo: a yank plan has no JSON form yet")
	}
	return f.planYank(context.Background(), tag, y, tokens, r)
}

// planYank says what retracting a release would change and, with out, saves it
// for `letsgo apply`.
func (f forge) planYank(ctx context.Context, tag string, y yankArgs, tokens diffTokens, r diffRun) error {
	m, err := f.resolveModuleRepo(ctx, tokens.Token)
	if err != nil {
		return err
	}
	previous, err := previousRelease(ctx, m.Client, m.Repo, tag, m.Scope.Prefix)
	if err != nil {
		return err
	}
	options := f.yankOptions(ctx, m, yankTarget{Tag: tag, Reason: y.reason, Previous: previous, KeepTap: y.keepTap}, tokens)

	fmt.Printf("retract %s from %s\n", tag, m.Repo)
	actions, err := apply.ObserveYank(ctx, options)
	if err != nil {
		return err
	}

	yankPlan := apply.YankPlan{
		Repo: m.Repo.Owner + "/" + m.Repo.Name, Tag: tag, Reason: y.reason, Previous: previous, Actions: actions,
	}
	r.Then = "letsgo yank " + tag
	r.Title = "letsgo plan: yank " + tag
	return finishPlan(actions, func(path string) (string, error) { return apply.SaveYank(yankPlan, path, version) }, r)
}

// applyYank retracts a release as a saved plan agreed: the forge and go.mod
// must still be as the plan found them, and only what it lists is written.
func (f forge) applyYank(ctx context.Context, file *plandiff.File, tokens diffTokens) error {
	m, err := f.resolveModuleRepo(ctx, tokens.Token)
	if err != nil {
		return err
	}
	if repo := m.Repo.Owner + "/" + m.Repo.Name; repo != file.Repo {
		return fmt.Errorf("letsgo: the plan is for %s, and this checkout is %s", file.Repo, repo)
	}

	// A plan that touches no tap file was made without one.
	keepTap := !apply.Touches(file.Actions, plandiff.KindTap)
	options := f.yankOptions(ctx, m, yankTarget{Tag: file.Tag, Reason: file.Reason, Previous: file.Previous, KeepTap: keepTap}, tokens)

	fmt.Println()
	result, err := apply.Yank(ctx, file, options, say)
	if err != nil {
		return err
	}
	reportNextSteps(result, file.Tag)
	return nil
}
