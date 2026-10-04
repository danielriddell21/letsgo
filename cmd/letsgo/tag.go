package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/danielriddell21/letsgo/internal/git"

	"github.com/danielriddell21/letsgo/internal/bump"
	"github.com/danielriddell21/letsgo/internal/discover"
)

func runTag(args []string) error {
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

	gitBin, err := gitBinary()
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

	repo := bump.Repo{Runner: loc.Runner, Module: loc.Module, Scope: scope, Global: machineConfig()}
	proposal, err := repo.ProposeFor(ctx, previous, bump.Forced(*major, *minor, *patch))
	if err != nil {
		return err
	}
	if *pre {
		proposal.Next = bump.NextPrerelease(tags, scope.Prefix, proposal.Next)
	}

	if *jsonOutput {
		return tagJSON(ctx, loc.Runner, scope.Prefix+proposal.Next, proposal, *yes)
	}

	return createTag(ctx, loc.Runner, scope.Prefix, proposal, previous, *warranted, *yes)
}

// tagJSON is `letsgo tag --json`: the proposal, wire-formed, with the full
// ref it names. A dry run unless create is set, in which case the tag is made
// first and the output says so, so a caller reads the ref instead of scraping
// prose for it.
func tagJSON(ctx context.Context, runner git.Runner, ref string, proposal bump.Proposal, create bool) error {
	if create {
		if runner.TagExists(ctx, ref) {
			return fmt.Errorf("%s already exists", ref)
		}
		if err := runner.CreateTag(ctx, ref, ref); err != nil {
			return err
		}
	}
	data, err := proposal.JSON(ref, create)
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

// createTag reports the proposal as text, then creates the tag unless
// --warranted finds nothing to signal a release or the operator declines.
func createTag(
	ctx context.Context, runner git.Runner, prefix string, proposal bump.Proposal, previous string, warranted, yes bool,
) error {
	tag := prefix + proposal.Next

	reportProposal(proposal, previous)

	// Unattended, the absence of a signal is an answer: nothing here claims to
	// be a release, so making one would put a version on a commit whose author
	// did not ask for it.
	if warranted && !proposal.Signalled() {
		fmt.Println("\n  nothing was tagged: no commit or API change calls for a release")
		return nil
	}

	if runner.TagExists(ctx, tag) {
		return fmt.Errorf("%s already exists", tag)
	}
	if !yes && !confirm(tag) {
		fmt.Println("\n  nothing was tagged")
		return nil
	}

	if err := runner.CreateTag(ctx, tag, tag); err != nil {
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
