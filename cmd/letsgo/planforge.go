package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/danielriddell21/letsgo/internal/apply"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/notes"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publication"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/release"
)

// newForgeClient is how a command reaches the forge. A variable so that a test
// can point it at a server of its own.
var newForgeClient = func(token string) *github.Client {
	client := github.New(token)
	client.UserAgent = "letsgo/" + version
	return client
}

// diffTokens are the credentials planDiff reads the forge with.
type diffTokens struct {
	Token, TapToken, ReleaseToken string
}

// planDiff builds the release into a scratch directory and reads the forge to
// say what releasing would change.
func planDiff(ctx context.Context, p *plan.Plan, tokens diffTokens) (*apply.Diff, error) {
	if !p.HasRepo {
		return nil, fmt.Errorf("letsgo: --diff needs a repository on a forge to compare against")
	}

	dir, err := os.MkdirTemp("", "letsgo-plan-")
	if err != nil {
		return nil, fmt.Errorf("letsgo: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	tokenValue, _ := plan.Token(tokens.Token)
	client := newForgeClient(tokenValue)
	repo := github.Repo{Owner: p.Repo.Owner, Name: p.Repo.Name}

	var info *github.RepoInfo
	if wantsRepoInfo(p) {
		info = describeRepo(ctx, client, repo)
	}

	result, err := release.Build(ctx, p, dir, version, info, func(format string, args ...any) {
		fmt.Printf("    ! "+format+"\n", args...)
	})
	if err != nil {
		return nil, fmt.Errorf("letsgo: %w", err)
	}

	manifestPath := filepath.Join(result.Dir, manifest.FileName)
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("letsgo: %w", err)
	}
	manifestSum, err := fileSum(manifestPath)
	if err != nil {
		return nil, err
	}
	notes, err := notes.Release(ctx, notesSource(p, client, repo, result.Manifest, manifestSum))
	if err != nil {
		return nil, err
	}

	actions, err := publication.Observe(ctx, publication.Options{
		Plan:  p,
		Forge: releaseClientFor(client, tokens.ReleaseToken, tokens.Token),
		Tap:   tapClientFor(client, tokens.TapToken, tokens.Token),
		Token: tokenValue, Repo: repo, Dir: dir, Result: result, Notes: notes, Info: info,
	})
	if err != nil {
		return nil, err
	}

	return &apply.Diff{
		Actions: actions, Manifest: manifestBytes, ManifestSHA256: "sha256:" + hex.EncodeToString(manifestSum),
	}, nil
}
