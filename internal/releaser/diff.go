package releaser

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/danielriddell21/letsgo/internal/apply"
	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/internal/notes"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publication"
	"github.com/danielriddell21/letsgo/internal/release"
)

// Diff builds the release into a scratch directory and reads the forge to say
// what releasing would change. It lives here rather than in apply because it
// builds, and apply must not import the build.
//
// It uses Options.Clients, Token, ToolVersion, Log and Warn; the plan is the
// one the caller has already resolved.
func Diff(ctx context.Context, p *plan.Plan, o Options) (*apply.Diff, error) {
	if err := o.Clients.require(); err != nil {
		return nil, err
	}
	if !p.HasRepo {
		return nil, fmt.Errorf("letsgo: --diff needs a repository on a forge to compare against")
	}

	dir, err := os.MkdirTemp("", "letsgo-plan-")
	if err != nil {
		return nil, fmt.Errorf("letsgo: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	repo := github.Repo{Owner: p.Repo.Owner, Name: p.Repo.Name}

	info := RepoInfo(ctx, o.Clients.Read, p, o.Log)

	result, err := release.Build(ctx, release.BuildOptions{
		Plan: p, Dir: dir, ToolVersion: o.ToolVersion, Repo: publication.ReleaseRepoInfo(info),
		Warnf: func(format string, args ...any) { o.log("    ! "+format, args...) },
	})
	if err != nil {
		return nil, fmt.Errorf("letsgo: %w", err)
	}

	manifestBytes, err := os.ReadFile(manifestFile(result.Dir))
	if err != nil {
		return nil, fmt.Errorf("letsgo: %w", err)
	}
	manifestSum, err := fileSum(manifestFile(result.Dir))
	if err != nil {
		return nil, err
	}
	text, err := notes.Release(ctx, o.notesSource(p, repo, result.Manifest, manifestSum))
	if err != nil {
		return nil, err
	}

	actions, err := publication.Observe(ctx, publication.Options{
		Plan: p, Forge: o.Clients.Release, Tap: o.Clients.Tap,
		Token: o.Token, Repo: repo, Dir: dir, Result: result, Notes: text, Info: info,
	})
	if err != nil {
		return nil, err
	}

	return &apply.Diff{
		Actions: actions, Manifest: manifestBytes, ManifestSHA256: "sha256:" + hex.EncodeToString(manifestSum),
	}, nil
}
