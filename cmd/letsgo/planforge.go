package main

import (
	"context"
	"crypto/sha1" //nolint:gosec // git names a blob by its SHA-1; this is not a security use
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/danielriddell21/letsgo/internal/brew"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/release"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

// forgeTargets are the things a release would touch, and how each would change.
type forgeTargets struct {
	Forge  publish.Forge
	Tap    brew.FileAPI
	Token  string
	Repo   github.Repo
	Dir    string
	Result *release.Result
	Notes  string
	Info   *github.RepoInfo
}

// diffForge reads the forge and says what `letsgo release` would do to it.
//
// It runs the same decisions the release runs — releaseOptions for the
// release and its assets, publishTapTo for the tap — against a view of the
// forge that can only be read, so the prediction cannot drift from the
// practice.
func diffForge(ctx context.Context, p *plan.Plan, t forgeTargets) ([]plandiff.Action, error) {
	actions, err := publish.Observe(ctx, releaseOptions(p, releaseRun{
		Forge: t.Forge, Repo: t.Repo, Dir: t.Dir, Result: t.Result, Notes: t.Notes,
		Draft: p.Config.Draft,
	}))
	if err != nil {
		return nil, err
	}

	if p.Config.Draft {
		return actions, nil
	}

	tap := &tapObserver{api: t.Tap}
	if err := publishTapTo(ctx, io.Discard, p, t.Result, tap, t.Repo, t.Info); err != nil {
		return nil, err
	}
	actions = append(actions, tap.actions()...)

	images, err := release.ObserveImages(ctx, t.Result.Images, t.Token)
	if err != nil {
		return nil, err
	}
	return append(actions, images...), nil
}

// tapObserver lets the tap's publishing decisions run and reports what they
// would write, without writing it.
type tapObserver struct {
	api  brew.FileAPI
	read []*observedFile
}

type observedFile struct {
	path     string
	observed string
	planned  string
	written  bool
}

func (t *tapObserver) ReadFile(ctx context.Context, repo github.Repo, path string) (*github.File, error) {
	file, err := t.api.ReadFile(ctx, repo, path)
	if err != nil {
		return nil, err
	}
	// Publishing a file may read it more than once, to decide and then to
	// write; it is one target however often it is looked at.
	for _, earlier := range t.read {
		if earlier.path == path {
			return file, nil
		}
	}
	seen := &observedFile{path: path}
	if file != nil {
		seen.observed = blobFingerprint(file.Content)
		seen.planned = seen.observed
	}
	t.read = append(t.read, seen)
	return file, nil
}

func (t *tapObserver) WriteFile(_ context.Context, _ github.Repo, in github.FileInput) error {
	for _, seen := range t.read {
		if seen.path == in.Path {
			seen.planned, seen.written = blobFingerprint(in.Content), true
			return nil
		}
	}
	return fmt.Errorf("letsgo: the tap was written at %s without being read first", in.Path)
}

func (t *tapObserver) actions() []plandiff.Action {
	out := make([]plandiff.Action, 0, len(t.read))
	for _, seen := range t.read {
		action := plandiff.Action{
			Kind: plandiff.KindTap, Target: seen.path, Observed: seen.observed, Planned: seen.planned,
		}
		switch {
		case !seen.written:
			action.Op = plandiff.Keep
		case seen.observed == "":
			action.Op = plandiff.Add
		default:
			action.Op = plandiff.Change
		}
		out = append(out, action)
	}
	return out
}

// blobFingerprint names content the way git names a blob, so a tap file can be
// compared with what the forge reports without fetching anything twice.
func blobFingerprint(content []byte) string {
	h := sha1.New() //nolint:gosec // see the import
	fmt.Fprintf(h, "blob %d\x00", len(content))
	_, _ = h.Write(content)
	return "blob:" + hex.EncodeToString(h.Sum(nil))
}

// newForgeClient is how a command reaches the forge. A variable so that a test
// can point it at a server of its own.
var newForgeClient = func(token string) *github.Client {
	client := github.New(token)
	client.UserAgent = "letsgo/" + version
	return client
}

// forgeDiff is what reading the forge against a fresh build came to.
type forgeDiff struct {
	Actions []plandiff.Action

	// Manifest is the manifest the build produced, and ManifestSHA256 its
	// digest: what an apply must reproduce.
	Manifest       []byte
	ManifestSHA256 string
}

// diffTokens are the credentials planDiff reads the forge with.
type diffTokens struct {
	Token, TapToken, ReleaseToken string
}

// planDiff builds the release into a scratch directory and reads the forge to
// say what releasing would change.
func planDiff(ctx context.Context, p *plan.Plan, tokens diffTokens) (*forgeDiff, error) {
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
	notes, err := releaseNotes(ctx, p, client, repo, result.Manifest, manifestSum)
	if err != nil {
		return nil, err
	}

	actions, err := diffForge(ctx, p, forgeTargets{
		Forge: releaseClientFor(client, tokens.ReleaseToken, tokens.Token),
		Tap:   tapClientFor(client, tokens.TapToken, tokens.Token),
		Token: tokenValue, Repo: repo, Dir: dir, Result: result, Notes: notes, Info: info,
	})
	if err != nil {
		return nil, err
	}

	return &forgeDiff{
		Actions: actions, Manifest: manifestBytes, ManifestSHA256: "sha256:" + hex.EncodeToString(manifestSum),
	}, nil
}
