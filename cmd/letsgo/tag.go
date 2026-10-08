package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/danielriddell21/letsgo/internal/git"

	"github.com/danielriddell21/letsgo/internal/bump"
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/discover"
)

func (f forge) runTag(args []string) error {
	fs := flag.NewFlagSet("tag", flag.ExitOnError)
	yes := fs.Bool("yes", false, "create the tag without asking")
	warranted := fs.Bool("warranted", false,
		"tag only if a commit or an API change calls for a release")
	major := fs.Bool("major", false, "force a major bump")
	minor := fs.Bool("minor", false, "force a minor bump")
	patch := fs.Bool("patch", false, "force a patch bump")
	pre := fs.Bool("pre", false, "propose a prerelease (-rc.N) instead of a stable version")
	jsonOutput := fs.Bool("json", false, "print the proposal as JSON; with --yes, create the tag and report its ref")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	ctx := context.Background()

	gitBin, err := f.gitBinary()
	if err != nil {
		return err
	}
	loc, err := discover.Locate(ctx, gitBin, ".")
	if err != nil {
		return err
	}
	if (!*jsonOutput || *yes) && !loc.Git.Clean {
		return fmt.Errorf("uncommitted changes; a tag names a commit, so commit first")
	}
	scope := loc.Scope

	tags, err := loc.Runner.Tags(ctx, scope.Prefix)
	if err != nil {
		return err
	}
	previous, _ := scope.LatestStableTag(tags, loc.Git.Tags...)

	module, err := releasedModule(loc.Module)
	if err != nil {
		return err
	}

	repo := bump.Repo{Runner: loc.Runner, Module: module, Scope: scope, Global: f.machine()}
	proposal, err := repo.ProposeFor(ctx, previous, bump.Forced(*major, *minor, *patch))
	if err != nil {
		return err
	}
	if *pre {
		proposal.Next = bump.NextPrerelease(tags, scope.Prefix, proposal.Next)
	}

	req := tagRequest{
		Runner: loc.Runner, Prefix: scope.Prefix, Proposal: proposal, Previous: previous,
		Warranted: *warranted, Yes: *yes,
	}
	if *jsonOutput {
		return tagJSON(ctx, req)
	}
	return createTag(ctx, req)
}

// tagRequest is a tag to propose and, unless it is a dry run, create.
type tagRequest struct {
	Runner   git.Runner
	Prefix   string
	Proposal bump.Proposal

	// Previous is the tag the proposal bumps from, for the report.
	Previous string

	// Warranted tags only when a commit or an API change calls for a release.
	// Yes creates the tag without asking.
	Warranted, Yes bool
}

// ref is the full tag the request names, scope prefix included.
func (r tagRequest) ref() string { return r.Prefix + r.Proposal.Next }

// tagJSON is `letsgo tag --json`: the proposal, wire-formed, with the full
// ref it names. A dry run unless the request says Yes, in which case the tag is
// made first and the output says so, so a caller reads the ref instead of
// scraping prose for it.
func tagJSON(ctx context.Context, req tagRequest) error {
	ref, create := req.ref(), req.Yes
	if create {
		if req.Runner.TagExists(ctx, ref) {
			return fmt.Errorf("%s already exists", ref)
		}
		if err := req.Runner.CreateTag(ctx, ref, ref); err != nil {
			return err
		}
	}
	data, err := req.Proposal.JSON(ref, create)
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

// createTag reports the proposal as text, then creates the tag unless
// --warranted finds nothing to signal a release or the operator declines.
func createTag(ctx context.Context, req tagRequest) error {
	tag := req.ref()

	reportProposal(req.Proposal, req.Previous)

	// Unattended, the absence of a signal is an answer: nothing here claims to
	// be a release, so making one would put a version on a commit whose author
	// did not ask for it.
	if req.Warranted && !req.Proposal.Signalled() {
		fmt.Println("\n  nothing was tagged: no commit or API change calls for a release")
		return nil
	}

	if req.Runner.TagExists(ctx, tag) {
		return fmt.Errorf("%s already exists", tag)
	}
	if !req.Yes && !confirm(tag) {
		fmt.Println("\n  nothing was tagged")
		return nil
	}

	if err := req.Runner.CreateTag(ctx, tag, tag); err != nil {
		return err
	}
	fmt.Printf("\n  tagged %s\n  push it with: git push origin %s\n", tag, tag)
	return nil
}

func reportProposal(p bump.Proposal, previous string) {
	from := previous
	if from == "" {
		from = "(no earlier tag)"
	}
	fmt.Printf("%s \u2192 %s  (%s)\n\n", from, p.Next, p.Level)

	width := 0
	for _, s := range p.Signals {
		width = max(width, len(s.Source))
	}
	for _, s := range p.Signals {
		fmt.Printf("  %-*s  %-6s %s\n", width, s.Source, s.Level, s.Detail)
	}

	// Worth showing rather than resolving silently: the two signals measure
	// different things, and where they differ one of them is usually telling
	// you something about the change you did not intend.
	if p.Disagree() {
		fmt.Printf("\n  the signals disagree; the larger is used\n")
	}
	for _, note := range p.Notes {
		fmt.Printf("\n  ! %s\n", note)
	}
}

func confirm(version string) bool {
	fmt.Printf("\n  create tag %s? [y/N] ", version)
	return readYes()
}

// readYes reads one answer from the terminal. Anything but an explicit yes is
// a no, including an unreadable stdin: a prompt nobody saw must not be taken
// as agreement.
func readYes() bool {
	answer, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}

// releasedModule is the module a release builds: the one letsgo.mod's module
// directive names, or the one beside it when there is no directive. The
// version is measured against that module, as plan resolves it, so a commit
// to a released nested module is read as a release signal rather than
// excluded as some other module's.
func releasedModule(m discover.Module) (discover.Module, error) {
	cfg, _, err := config.Load(m.Dir)
	if errors.Is(err, config.ErrNotFound) {
		return m, nil
	}
	if err != nil {
		return discover.Module{}, err
	}
	if cfg.ModuleDir == "" {
		return m, nil
	}

	dir := filepath.Join(m.Dir, filepath.FromSlash(cfg.ModuleDir))
	released, err := discover.FindModule(dir)
	if err != nil {
		return discover.Module{}, fmt.Errorf("module %s: %w", cfg.ModuleDir, err)
	}
	// FindModule walks up, so a directory with no go.mod of its own would
	// resolve to the module above it, which is not the one that was named.
	if released.Dir != dir {
		return discover.Module{}, fmt.Errorf("module %s: no go.mod in that directory", cfg.ModuleDir)
	}
	return released, nil
}
