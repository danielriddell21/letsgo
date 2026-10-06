package releaser

import (
	"context"
	"fmt"
	"time"

	"github.com/danielriddell21/letsgo/internal/apply"
	"github.com/danielriddell21/letsgo/internal/forgerelease"
	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/internal/notes"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publication"
	"github.com/danielriddell21/letsgo/manifest"
)

// Release builds and publishes a release, or with Options.Snapshot rehearses
// one: build, hold the build to a saved plan, guard the forge, then publish.
// Only the thing that writes to the world is exchanged for a rehearsal;
// everything before it is identical.
func Release(ctx context.Context, o Options) (*Result, error) {
	if err := o.Clients.require(); err != nil {
		return nil, err
	}
	started := time.Now()

	b, err := build(ctx, o, started, "nothing was built or published")
	if err != nil {
		return nil, err
	}
	p, result := b.plan, b.result
	o.log("\n  built %d files", len(result.Files))

	applied, err := apply.HoldAndStamp(o.Applied, p, result, o.say)
	if err != nil {
		return nil, err
	}

	// After the plan is held to its file, which records the config's draft and
	// not a flag given at apply time. The tap and image publishers read the
	// plan, so they hold back for it the same way they do for `draft = true`
	// in the config.
	if o.Draft {
		p.MarkDraft()
	}

	repo := github.Repo{Owner: p.Repo.Owner, Name: p.Repo.Name}

	manifestSum, err := fileSum(manifestFile(result.Dir))
	if err != nil {
		return nil, err
	}
	notesText, err := notes.Release(ctx, o.notesSource(p, repo, result.Manifest, manifestSum))
	if err != nil {
		return nil, err
	}

	targets := publication.Options{
		Plan: p, Result: result, Dir: b.dir, Repo: repo, Forge: o.Clients.Release, Tap: o.Clients.Tap,
		Info: b.info, Notes: notesText, Token: o.Token, Snapshot: o.Snapshot, Out: o.out(),
	}
	forge, tapAPI, err := apply.Guard(ctx, applied, targets, o.say)
	if err != nil {
		return nil, err
	}
	if o.Snapshot {
		o.log("\n  rehearsal: the calls below would be made, and are not")
		recorder := forgerelease.NewRecorder(o.out())
		forge, tapAPI = recorder, recorder
	}

	targets.Forge, targets.Tap, targets.Append = forge, tapAPI, o.AppendNotes
	done, err := publication.Publish(ctx, targets)
	if err != nil {
		return nil, err
	}

	res := &Result{Plan: p, Dir: b.dir, Build: result, Info: b.info, Snapshot: o.Snapshot, Took: Took(started)}
	if !o.Snapshot {
		res.URL = done.Forge.Release.HTMLURL
	}
	return res, nil
}

// say writes one progress line.
func (o Options) say(format string, args ...any) { o.log(format, args...) }

// notesSource is what the release notes are written from. What it skips goes
// to Options.Warn, which is where diagnostics belong.
func (o Options) notesSource(
	p *plan.Plan, repo github.Repo, current *manifest.Manifest, manifestSum []byte,
) notes.Source {
	return notes.Source{
		Plan: p, Client: o.Clients.Read, Repo: repo, Manifest: current, ManifestSum: manifestSum,
		Logf: func(format string, args ...any) {
			if o.Warn != nil {
				fmt.Fprintf(o.Warn, "  "+format+"\n", args...)
			}
		},
	}
}
