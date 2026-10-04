package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/danielriddell21/letsgo/internal/credential"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/plugin"
	"github.com/danielriddell21/letsgo/internal/yank"
)

func (f forge) runYank(args []string) error {
	fs := flag.NewFlagSet("yank", flag.ExitOnError)
	var y yankArgs
	y.bind(fs)
	token := fs.String("token", "", "forge token (default: $GITHUB_TOKEN or $GH_TOKEN)")
	tapToken := fs.String("tap-token", "", tapTokenUsage)
	yes := fs.Bool("yes", false, "retract without asking")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errUsage("letsgo yank <tag> [--reason \"...\"]")
	}
	tag := fs.Arg(0)

	ctx := context.Background()

	set := credentials(ctx, credential.Flags{Token: *token, TapToken: *tapToken})
	m, err := f.resolveModuleRepo(ctx, set.Forge)
	if err != nil {
		return err
	}

	previous, err := previousRelease(ctx, m.Client, m.Repo, tag, m.Scope.Prefix)
	if err != nil {
		return err
	}

	options := f.yankOptions(ctx, m, yankTarget{Tag: tag, Reason: y.reason, Previous: previous, KeepTap: y.keepTap},
		set)
	options.Logf = func(format string, args ...any) { fmt.Printf("  "+format+"\n", args...) }

	reportYank(tag, m.Repo, previous, options)
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
func previousRelease(ctx context.Context, client *github.Client, repo github.Repo, tag, prefix string) (string, error) {
	tags, err := client.Tags(ctx, repo, 100)
	if err != nil {
		return "", err
	}
	return yank.PreviousOf(tags, tag, prefix), nil
}

// tapFor reads the configured Homebrew tap, if there is one, and the
// tap-files plugin pinned alongside it. A malformed config is not worth
// failing a retraction over: the tap is the least important of the four
// things yank does.
//
// The client is the tap's rather than the release's, for the same reason the
// release path separates them: rolling a formula back writes to the tap only.
func tapFor(o *yank.Options, moduleDir string, client *github.Client) {
	p, err := plan.Resolve(context.Background(), plan.Options{Dir: moduleDir, Snapshot: true, AllowDirty: true})
	if err != nil || p.Tap == (github.Repo{}) {
		return
	}
	o.Tap, o.TapAPI, o.TapFilesPlugin = p.Tap, client, p.Plugins[plugin.HookTapFiles]
	o.PluginRoot, o.PluginsDir = p.RootDir, p.PluginsDir()
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

func envList() string { return strings.Join(credential.EnvVars, " or ") }

// brewCaveats reads the formula's caveats from the config, so a rollback
// republishes the formula the release published rather than one missing a
// section. Nothing here is fatal: a repository with no config, or one whose
// config no longer parses, simply has nothing to say.
func brewCaveats(moduleDir string) string {
	cfg, _, err := config.Load(moduleDir)
	if err != nil {
		return ""
	}
	return cfg.BrewCaveats
}
