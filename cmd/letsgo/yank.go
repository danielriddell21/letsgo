package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/danielriddell21/letsgo/internal/brew"
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/diff"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/yank"
)

func runYank(args []string) error {
	fs := flag.NewFlagSet("yank", flag.ExitOnError)
	reason := fs.String("reason", "", "why the release should not be used; shown by `go list -m -retracted`")
	token := fs.String("token", "", "forge token (default: $GITHUB_TOKEN or $GH_TOKEN)")
	yes := fs.Bool("yes", false, "retract without asking")
	keepTap := fs.Bool("keep-tap", false, "leave the Homebrew formula pointing at the retracted release")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errUsage("letsgo yank <tag> [--reason \"...\"]")
	}
	tag := fs.Arg(0)

	ctx := context.Background()

	module, err := discover.FindModule(".")
	if err != nil {
		return fmt.Errorf("letsgo: %w", err)
	}
	found, err := discover.FindRepo(ctx, module.Dir)
	if err != nil {
		return fmt.Errorf("letsgo: %w", err)
	}
	repo := github.Repo{Owner: found.Owner, Name: found.Name}

	tokenValue, _ := plan.Token(*token)
	if tokenValue == "" {
		return fmt.Errorf("letsgo: no token; set %s", envList())
	}
	client := github.New(tokenValue)
	client.UserAgent = "letsgo/" + version

	previous, err := previousRelease(ctx, client, repo, tag)
	if err != nil {
		return err
	}

	options := yank.Options{
		Client:   client,
		Repo:     repo,
		Tag:      tag,
		Reason:   *reason,
		GoMod:    filepath.Join(module.Dir, "go.mod"),
		Project:  module.Name,
		Caveats:  brewCaveats(module.Dir),
		Previous: previous,
		Manifests: func(ctx context.Context, tag string) (*manifest.Manifest, error) {
			return diff.Fetch(ctx, client, repo, tag)
		},
		Logf: func(format string, args ...any) { fmt.Printf("  "+format+"\n", args...) },
	}

	if !*keepTap {
		options.Tap, options.TapAPI = tapFor(module.Dir, client)
	}

	reportYank(tag, repo, previous, options)
	if !*yes && !confirmYank(tag) {
		fmt.Println("\n  nothing was retracted")
		return nil
	}

	fmt.Println()
	result, err := yank.Run(ctx, options)
	if err != nil {
		return err
	}

	reportNextSteps(result, tag)
	return nil
}

// previousRelease finds the release the formula should roll back to.
func previousRelease(ctx context.Context, client *github.Client, repo github.Repo, tag string) (string, error) {
	tags, err := client.Tags(ctx, repo, 100)
	if err != nil {
		return "", err
	}
	return yank.PreviousOf(tags, tag), nil
}

// tapFor reads the configured Homebrew tap, if there is one. A malformed
// config is not worth failing a retraction over: the formula is the least
// important of the four things yank does.
func tapFor(moduleDir string, client *github.Client) (github.Repo, brew.FileAPI) {
	p, err := plan.Resolve(context.Background(), plan.Options{Dir: moduleDir, Snapshot: true, AllowDirty: true})
	if err != nil || p.Tap == (github.Repo{}) {
		return github.Repo{}, nil
	}
	return p.Tap, client
}

func reportYank(tag string, repo github.Repo, previous string, o yank.Options) {
	fmt.Printf("retract %s from %s\n\n", tag, repo)

	fmt.Println("  this will")
	fmt.Printf("    · mark the %s release as a prerelease, with a notice at the top\n", tag)
	fmt.Printf("    · add `retract %s` to go.mod\n", tag)
	if o.Tap != (github.Repo{}) && previous != "" {
		fmt.Printf("    · point %s back at %s\n", o.Tap, previous)
	}
	if o.Reason == "" {
		fmt.Println("\n  ! no --reason given; `go list -m -retracted` will show nothing useful")
	}
}

// reportNextSteps says the part that decides whether any of this worked.
func reportNextSteps(result *yank.Result, tag string) {
	fmt.Printf("\n  %s is retracted, and nobody knows it yet.\n\n", tag)

	fmt.Println("  A retract directive only takes effect once it is published in a later")
	fmt.Println("  version. Until you tag one, the proxy still serves the old go.mod and")
	fmt.Printf("  `go get` will keep selecting %s.\n\n", tag)

	fmt.Println("    git add go.mod")
	fmt.Printf("    git commit -m \"retract %s\"\n", tag)
	fmt.Printf("    letsgo tag --patch      # %s\n", result.Next)
	fmt.Println("    letsgo release")
}

func confirmYank(tag string) bool {
	fmt.Printf("\n  retract %s? [y/N] ", tag)
	return readYes()
}

func envList() string { return "GITHUB_TOKEN or GH_TOKEN" }

// brewCaveats reads the formula's caveats from the config, so a rollback
// republishes the formula the release published rather than one missing a
// section. Nothing here is fatal: a repository with no config, or one whose
// config no longer parses, simply has nothing to say.
func brewCaveats(moduleDir string) string {
	data, err := os.ReadFile(filepath.Join(moduleDir, plan.ConfigFile))
	if err != nil {
		return ""
	}
	file, err := config.Parse(plan.ConfigFile, data)
	if err != nil {
		return ""
	}
	cfg, err := config.Decode(file)
	if err != nil {
		return ""
	}
	return cfg.BrewCaveats
}
