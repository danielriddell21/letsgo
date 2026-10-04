package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/danielriddell21/letsgo/internal/diff"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/manifest"
)

// runDiff compares two releases.
//
// Both sides are read from manifests rather than from the repository, so this
// works against releases of projects that are not checked out, and says what
// the artifacts actually did rather than what the commit messages claimed.
func (f forge) runDiff(args []string) error {
	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	token := fs.String("token", "", "forge token (default: $GITHUB_TOKEN or $GH_TOKEN)")
	repoFlag := fs.String("repo", "", "repository to compare in as owner/name (default: this repository's origin)")
	format := fs.String("format", "text", "output format: text, md, or json")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *format != "text" && *format != "md" && *format != "json" {
		return errUsage(fmt.Sprintf("letsgo diff: unknown --format %q; want text, md, or json", *format))
	}
	if fs.NArg() == 0 || fs.NArg() > 2 {
		return errUsage("letsgo diff <from> [to]\n" +
			"  each side is a tag, or a path to a letsgo.json; to defaults to the latest release")
	}

	ctx := context.Background()

	// The forge is only consulted for sides given as tags, so comparing two
	// local manifests needs neither a network nor a token.
	var (
		client *github.Client
		repo   github.Repo
		scope  discover.Scope
	)
	from, to := fs.Arg(0), fs.Arg(1)
	if !isManifestPath(from) || !isManifestPath(to) {
		gitBin, err := gitBinary()
		if err != nil {
			return err
		}
		var dir string
		if repo, dir, err = targetRepo(ctx, gitBin, *repoFlag); err != nil {
			return err
		}
		prefix, err := scopePrefix(ctx, gitBin, *repoFlag, dir)
		if err != nil {
			return err
		}
		scope = discover.Scope{Prefix: prefix}
		tokenValue, _ := plan.Token(context.Background(), machineConfig(), *token)
		client = f.client(tokenValue)
	}

	before, err := loadManifest(ctx, client, repo, scope, from)
	if err != nil {
		return err
	}
	after, err := loadManifest(ctx, client, repo, scope, to)
	if err != nil {
		return err
	}

	return printDiff(diff.Compare(before, after), *format)
}

// printDiff renders a comparison in the requested format. format is already
// validated by the time this runs.
func printDiff(result *diff.Result, format string) error {
	switch format {
	case "md":
		fmt.Print(result.Markdown())
	case "json":
		data, err := result.JSON()
		if err != nil {
			return fmt.Errorf("letsgo diff: %w", err)
		}
		fmt.Println(string(data))
	default:
		fmt.Print(result)
	}
	return nil
}

// loadManifest resolves one side of a diff, which is either a file on disk or
// a tag on the forge. An empty reference means the latest release.
func loadManifest(ctx context.Context, client *github.Client, repo github.Repo, scope discover.Scope, ref string) (*manifest.Manifest, error) {
	if isManifestPath(ref) {
		return manifest.ReadStrict(ref)
	}
	return diff.Fetch(ctx, client, repo, scope, ref)
}

// isManifestPath reports whether a reference names a local manifest rather
// than a tag. Tags do not exist on disk, so the file system decides: this
// keeps a tag named like a path from being misread, and the reverse.
func isManifestPath(ref string) bool {
	if ref == "" {
		return false
	}
	info, err := os.Stat(ref)
	return err == nil && info.Mode().IsRegular()
}
