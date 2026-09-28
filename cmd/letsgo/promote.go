package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/promote"
	"github.com/danielriddell21/letsgo/internal/publish/github"
)

// runPromote rebuilds a prerelease as a stable release: see
// docs/hld/promote.md and docs/pbs/promote.md for the five steps this
// performs, and internal/promote for the implementation.
func runPromote(args []string) error {
	fs := flag.NewFlagSet("promote", flag.ExitOnError)
	token := fs.String("token", "", "forge token (default: $GITHUB_TOKEN or $GH_TOKEN)")
	tapToken := fs.String("tap-token", "", tapTokenUsage)
	releaseToken := fs.String("release-token", "", releaseTokenUsage)
	appendNotes := fs.Bool("append-notes", false, "add the notes after an existing release description instead of replacing it")
	yes := fs.Bool("yes", false, "promote without asking")
	work := fs.String("work", "", "scratch directory for the rebuild (default: a temporary one)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errUsage("letsgo promote <rc-tag>")
	}
	rcTag := fs.Arg(0)

	ctx := context.Background()
	started := time.Now()

	module, err := discover.FindModule(".")
	if err != nil {
		return fmt.Errorf("letsgo: %w", err)
	}
	found, err := discover.FindRepo(ctx, module.Dir)
	if err != nil {
		return fmt.Errorf("letsgo: %w", err)
	}
	repo := github.Repo{Owner: found.Owner, Name: found.Name}

	git, err := discover.FindGit(ctx, module.Dir)
	if err != nil {
		return fmt.Errorf("letsgo: %w", err)
	}
	scope, err := discover.NewScope(git.TopLevel, module.Dir)
	if err != nil {
		return fmt.Errorf("letsgo: %w", err)
	}

	tokenValue, _ := plan.Token(*token)
	if tokenValue == "" {
		return fmt.Errorf("letsgo: no token; set %s", envList())
	}
	client := github.New(tokenValue)
	client.UserAgent = "letsgo/" + version

	// The release itself, and the tap, each get their own client exactly as
	// `letsgo release` splits them: the RC and the stable release are the
	// same forge object regardless, but the tap may live in a repository the
	// plain token cannot write to.
	releaseClient := releaseClientFor(client, *releaseToken, *token)
	tapClient := tapClientFor(client, *tapToken, *token)

	workDir := *work
	if workDir == "" {
		workDir, err = os.MkdirTemp("", "letsgo-promote-")
		if err != nil {
			return fmt.Errorf("letsgo: scratch directory: %w", err)
		}
		defer func() { _ = os.RemoveAll(workDir) }()
	}

	fmt.Printf("promote %s in %s\n\n", rcTag, repo)
	fmt.Println("  this will")
	fmt.Println("    · restore the RC to a prerelease, unconditionally")
	fmt.Println("    · tag its commit with the stable version")
	fmt.Println("    · rebuild that commit and compare it against the RC's own manifest")
	fmt.Println("    · publish the stable release, marked latest, from the rebuilt assets")
	fmt.Println("    · publish the Homebrew formula and the container image, if configured")
	if !*yes && !confirmPromote(rcTag) {
		fmt.Println("\n  nothing was promoted")
		return nil
	}
	fmt.Println()

	info := tapRepoInfo(ctx, module.Dir, client)

	result, err := promote.Run(ctx, promote.Options{
		Client:      releaseClient,
		Repo:        repo,
		RCTag:       rcTag,
		Dir:         git.TopLevel,
		ModuleDir:   module.Dir,
		Prefix:      scope.Prefix,
		Shallow:     git.Shallow,
		ToolVersion: version,
		RepoInfo:    info,
		WorkDir:     workDir,
		AppendNotes: *appendNotes,
		Logf: func(format string, args ...any) {
			fmt.Printf("  "+format+"\n", args...)
		},
	})
	if err != nil {
		return err
	}
	reportPublished(result.Published)

	if err := publishTap(ctx, result.Plan, result.Build, tapClient, repo, info); err != nil {
		return err
	}
	if err := publishImages(ctx, result.Plan, result.Build, tokenValue, false); err != nil {
		return err
	}

	fmt.Printf("\n  promoted in %s\n  %s\n", took(started), result.Published.Release.HTMLURL)
	return nil
}

// tapRepoInfo reads the repository's description, licence and homepage for a
// formula or a tap-files plugin's cask, but only when there is a tap to write
// one into — mirroring tapFor in yank.go, which probes the same question the
// same way before spending an API call on an answer nothing will use.
func tapRepoInfo(ctx context.Context, moduleDir string, client *github.Client) *github.RepoInfo {
	p, err := plan.Resolve(ctx, plan.Options{Dir: moduleDir, Snapshot: true, AllowDirty: true})
	if err != nil || !wantsRepoInfo(p) {
		return nil
	}
	return describeRepo(ctx, client, github.Repo{Owner: p.Repo.Owner, Name: p.Repo.Name})
}

func confirmPromote(tag string) bool {
	fmt.Printf("\n  promote %s? [y/N] ", tag)
	return readYes()
}
