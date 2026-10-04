package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/danielriddell21/letsgo/internal/bump"
	"github.com/danielriddell21/letsgo/internal/changelog"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/gate"
	"github.com/danielriddell21/letsgo/internal/gobuild"
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

	module, err := discover.FindModule(".")
	if err != nil {
		return err
	}
	gitBin, err := gitBinary()
	if err != nil {
		return err
	}
	git, err := discover.FindGit(ctx, gitBin, module.Dir)
	if err != nil {
		return err
	}
	if (!*jsonOutput || *yes) && !git.Clean {
		return fmt.Errorf("uncommitted changes; a tag names a commit, so commit first")
	}
	scope, err := discover.NewScope(git.TopLevel, module.Dir)
	if err != nil {
		return err
	}

	tags, err := discover.Tags(ctx, gitBin, module.Dir, scope.Prefix)
	if err != nil {
		return err
	}
	previous, _ := scope.LatestStableTag(tags, git.Tags...)

	proposal, err := proposeVersion(ctx, gitBin, module, scope, previous, forced(*major, *minor, *patch))
	if err != nil {
		return err
	}
	if *pre {
		proposal.Next = nextPrerelease(tags, scope.Prefix, proposal.Next)
	}

	if *jsonOutput {
		return tagJSON(ctx, gitBin, module.Dir, scope.Prefix+proposal.Next, proposal, *yes)
	}

	return createTag(ctx, gitBin, module.Dir, scope.Prefix, proposal, previous, *warranted, *yes)
}

// tagJSON is `letsgo tag --json`: the proposal, wire-formed, with the full
// ref it names. A dry run unless create is set, in which case the tag is made
// first and the output says so, so a caller reads the ref instead of scraping
// prose for it.
func tagJSON(ctx context.Context, gitBin, dir, ref string, proposal bump.Proposal, create bool) error {
	if create {
		if discover.TagExists(ctx, gitBin, dir, ref) {
			return fmt.Errorf("%s already exists", ref)
		}
		if err := discover.CreateTag(ctx, gitBin, dir, ref, ref); err != nil {
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
	ctx context.Context, gitBin, dir, prefix string, proposal bump.Proposal, previous string, warranted, yes bool,
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

	if discover.TagExists(ctx, gitBin, dir, tag) {
		return fmt.Errorf("%s already exists", tag)
	}
	if !yes && !confirm(tag) {
		fmt.Println("\n  nothing was tagged")
		return nil
	}

	if err := discover.CreateTag(ctx, gitBin, dir, tag, tag); err != nil {
		return err
	}
	fmt.Printf("\n  tagged %s\n  push it with: git push origin %s\n", tag, tag)
	return nil
}

// nextPrerelease appends an auto-incrementing "-rc.N" suffix to a proposed
// base version. It scans the existing tags for the highest N already used
// under that exact base, so repeated --pre runs advance rc.1, rc.2, ...
// instead of colliding on the same candidate.
func nextPrerelease(tags []string, prefix, base string) string {
	want := prefix + base + "-rc."
	n := 0
	for _, tag := range tags {
		suffix, ok := strings.CutPrefix(tag, want)
		if !ok {
			continue
		}
		if v, err := strconv.Atoi(suffix); err == nil && v > n {
			n = v
		}
	}
	return fmt.Sprintf("%s-rc.%d", base, n+1)
}

// forced returns the level a flag demands, or bump.None for no flag.
func forced(major, minor, patch bool) bump.Level {
	switch {
	case major:
		return bump.Major
	case minor:
		return bump.Minor
	case patch:
		return bump.Patch
	default:
		return bump.None
	}
}

// proposeVersion gathers both signals and combines them.
//
// previous is the real git tag, prefixed exactly as it exists in the
// repository — Commits and checkoutForDiff need that to resolve it — but the
// version bump.Propose computes is a plain "vX.Y.Z", so scope.Prefix is
// stripped before it reaches the semver parser.
func proposeVersion(
	ctx context.Context, gitBin string, module discover.Module, scope discover.Scope, previous string, force bump.Level,
) (bump.Proposal, error) {
	previousVersion := strings.TrimPrefix(previous, scope.Prefix)

	if force != bump.None {
		return bump.Propose(previousVersion, module.Path,
			bump.Signal{Source: "you", Level: force, Detail: "requested on the command line"})
	}

	nested, err := discover.NestedModuleDirs(module.Dir)
	if err != nil {
		return bump.Proposal{}, err
	}
	commits, err := discover.Commits(ctx, gitBin, module.Dir, previous, "HEAD", nested...)
	if err != nil {
		return bump.Proposal{}, err
	}
	notes := changelog.Build(previous, "", commits)

	// The API signal needs an earlier tree to compare against, and something
	// importable to compare. Whatever stopped it is carried into the signal
	// rather than swallowed: a signal that dropped out leaves the version
	// decided by commit messages alone, and the report should say so.
	var (
		changes []gate.Change
		apiErr  = errors.New("no earlier release to compare against")
	)
	if previous != "" {
		global := machineConfig()
		goBin, _, _ := gobuild.Toolchain(global)
		old, cleanup, err := checkoutForDiff(ctx, gitBin, module.Dir, previous, scope.Dir)
		if err != nil {
			apiErr = err
		} else {
			defer cleanup()
			changes, apiErr = gate.APIDiff(ctx, global, goBin, old, module.Dir)
		}
	}

	return bump.Propose(previousVersion, module.Path,
		bump.FromAPI(changes, apiErr),
		bump.FromCommits(notes.Entries))
}

// checkoutForDiff checks out tag into a scratch worktree and returns the
// module's own directory within it — a worktree always holds the whole
// repository, so a module nested in it (relDir, slash-separated) is compared
// at <worktree>/relDir, never at the worktree's own root.
func checkoutForDiff(ctx context.Context, gitBin, repoDir, tag, relDir string) (string, func(), error) {
	base, err := os.MkdirTemp("", "letsgo-tag-")
	if err != nil {
		return "", nil, fmt.Errorf("letsgo: scratch directory: %w", err)
	}
	worktree := filepath.Join(base, "previous")
	if err := discover.AddWorktree(ctx, gitBin, repoDir, worktree, tag); err != nil {
		_ = os.RemoveAll(base)
		return "", nil, err
	}
	return filepath.Join(worktree, filepath.FromSlash(relDir)), func() {
		_ = discover.RemoveWorktree(ctx, gitBin, repoDir, worktree)
		_ = os.RemoveAll(base)
	}, nil
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
