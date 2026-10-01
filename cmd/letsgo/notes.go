package main

import (
	"fmt"
	"os"

	"github.com/danielriddell21/letsgo/internal/notes"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/manifest"
)

// notesSource is what the release notes are written from. What it skips goes
// to stderr, which is where diagnostics belong.
func notesSource(
	p *plan.Plan, client *github.Client, repo github.Repo, current *manifest.Manifest, manifestSum []byte,
) notes.Source {
	return notes.Source{
		Plan: p, Client: client, Repo: repo, Manifest: current, ManifestSum: manifestSum,
		Logf: func(format string, args ...any) { fmt.Fprintf(os.Stderr, "  "+format+"\n", args...) },
	}
}
