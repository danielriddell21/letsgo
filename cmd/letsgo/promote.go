package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/danielriddell21/letsgo/internal/credential"

	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/promote"
	"github.com/danielriddell21/letsgo/internal/releaser"
)

// runPromote rebuilds a prerelease as a stable release: see
// docs/hld/promote.md and docs/pbs/promote.md for the five steps this
// performs, and internal/promote for the implementation.
func (f forge) runPromote(args []string) error {
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

	set := credentials(ctx, credential.Flags{Token: *token, TapToken: *tapToken, ReleaseToken: *releaseToken})
	m, err := f.resolveModuleRepo(ctx, set.Forge)
	if err != nil {
		return err
	}
	repo, git, scope, client, tokenValue := m.Repo, m.Git, m.Scope, m.Client, m.Token

	// The release itself, and the tap, each get their own client exactly as
	// `letsgo release` splits them: the RC and the stable release are the
	// same forge object regardless, but the tap may live in a repository the
	// plain token cannot write to.
	releaseClient := f.splitClient(client, set.Forge, set.Release)
	tapClient := f.tapClient(client, set)

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

	info := tapRepoInfo(ctx, m.Module.Dir, client)

	result, err := promote.Run(ctx, promote.Options{
		Client:      releaseClient,
		Repo:        repo,
		RCTag:       rcTag,
		Dir:         git.TopLevel,
		GitBin:      m.GitBin,
		ModuleDir:   m.Module.Dir,
		Prefix:      scope.Prefix,
		Shallow:     git.Shallow,
		ToolVersion: version,
		RepoInfo:    info,
		WorkDir:     workDir,
		AppendNotes: *appendNotes,
		Tap:         tapClient,
		Token:       tokenValue,
		Out:         os.Stdout,
		Logf: func(format string, args ...any) {
			fmt.Printf("  "+format+"\n", args...)
		},
	})
	if err != nil {
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
	if err != nil || !releaser.WantsRepoInfo(p) {
		return nil
	}
	return releaser.DescribeRepo(ctx, client, github.Repo{Owner: p.Repo.Owner, Name: p.Repo.Name}, os.Stdout)
}

func confirmPromote(tag string) bool {
	fmt.Printf("\n  promote %s? [y/N] ", tag)
	return readYes()
}
