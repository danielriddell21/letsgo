package publication

import (
	"context"
	"crypto/sha1" //nolint:gosec // git names a blob by its SHA-1; this is not a security use
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"github.com/danielriddell21/letsgo/internal/brew"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/publish/github"
	"github.com/danielriddell21/letsgo/internal/release"
	plandiff "github.com/danielriddell21/letsgo/plan"
)

// publishTap writes a formula to the configured Homebrew tap, one per command
// the module builds, plus that formula's @next: a stable release writes both,
// a prerelease writes @next only.
//
// A module with several commands gets several formulas rather than a refusal:
// a formula installs one archive per platform, so `alpha` and `beta` cannot
// share one, and `Formula/alpha.rb` beside `Formula/beta.rb` is what a tap is
// shaped to hold anyway.
//
// info is what the caller already read from the forge for the tap-files hook;
// passed in rather than read again so the description is asked for once, not
// once per publisher.
func publishTap(ctx context.Context, out io.Writer, p *plan.Plan, result *release.Result, api brew.FileAPI, repo github.Repo, info *github.RepoInfo) error {
	if p.Tap == (github.Repo{}) {
		return nil
	}

	// Only a published release serves assets from the download URLs a formula
	// (or a tap-files plugin's output) names. Pointing a tap at a draft would
	// produce a cask or formula that resolves to a 404 for everyone but its
	// author.
	if p.Draft() {
		fmt.Fprintln(out, "  ! skipped the Homebrew tap: a draft release serves no public assets")
		return nil
	}

	if names := p.VariantNames(); len(names) > 0 {
		fmt.Fprintf(out, "  ! no formula for variant %s: a variant's package is the repository's to choose\n",
			strings.Join(names, ", "))
	}

	// A prerelease's formula would overwrite the stable tap entry that
	// `brew install foo` relies on, so it writes @next only — pointing at
	// whichever of the newest prerelease and the newest stable is newer.
	// TapFiles are release output the same way the formula is, so they follow
	// the same rule: skipped for a prerelease, written for a stable release.
	prerelease := isPrerelease(p)
	if prerelease {
		fmt.Fprintln(out, "  ! a prerelease must not overwrite the stable formula: writing @next only")
	}

	for _, formula := range formulas(p, result, repo, info) {
		if !prerelease {
			published, err := brew.Publish(ctx, api, p.Tap, formula)
			if err != nil {
				return fmt.Errorf("publishing formula %s: %w", formula.Name, err)
			}
			fmt.Fprintf(out, "  %s %s in %s\n", published.Status, published.Path, p.Tap)
		}

		next := formula
		next.Name = brew.NextName(formula.Name)
		published, err := brew.PublishNext(ctx, api, p.Tap, next)
		if err != nil {
			return fmt.Errorf("publishing formula %s: %w", next.Name, err)
		}
		fmt.Fprintf(out, "  %s %s in %s\n", published.Status, published.Path, p.Tap)
	}

	if prerelease {
		return nil
	}

	// Written in the same tap update as the formula: whatever a tap-files
	// plugin rendered was already validated back when the release was built,
	// so nothing here can still fail on the plugin's account.
	for _, f := range result.TapFiles {
		published, err := brew.PublishFile(ctx, api, p.Tap, f.Path, []byte(f.Content),
			fmt.Sprintf("%s %s", p.Project, p.Version))
		if err != nil {
			return fmt.Errorf("publishing %s: %w", f.Path, err)
		}
		fmt.Fprintf(out, "  %s %s in %s\n", published.Status, published.Path, p.Tap)
	}
	return nil
}

// formulas builds the release's formulas from its manifest, the one builder a
// yank's rollback uses too. See brew.FormulasFor.
func formulas(p *plan.Plan, result *release.Result, repo github.Repo, info *github.RepoInfo) []brew.Formula {
	return brew.FormulasFor(result.Manifest, repo, info, p.Project, p.BrewCaveats())
}

// TapObserver lets the tap's publishing decisions run and reports what they
// would write, without writing it.
type TapObserver struct {
	api  brew.FileAPI
	read []*observedFile
}

type observedFile struct {
	path     string
	observed string
	planned  string
	written  bool
}

func (t *TapObserver) ReadFile(ctx context.Context, repo github.Repo, path string) (*github.File, error) {
	file, err := t.api.ReadFile(ctx, repo, path)
	if err != nil {
		return nil, fmt.Errorf("reading %s from the tap: %w", path, err)
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
		seen.observed = BlobFingerprint(file.Content)
		seen.planned = seen.observed
	}
	t.read = append(t.read, seen)
	return file, nil
}

func (t *TapObserver) WriteFile(_ context.Context, _ github.Repo, in github.FileInput) error {
	for _, seen := range t.read {
		if seen.path == in.Path {
			seen.planned, seen.written = BlobFingerprint(in.Content), true
			return nil
		}
	}
	return fmt.Errorf("letsgo: the tap was written at %s without being read first", in.Path)
}

func (t *TapObserver) Actions() []plandiff.Action {
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

// BlobFingerprint names content the way git names a blob, so a tap file can be
// compared with what the forge reports without fetching anything twice.
func BlobFingerprint(content []byte) string {
	h := sha1.New() //nolint:gosec // see the import
	fmt.Fprintf(h, "blob %d\x00", len(content))
	_, _ = h.Write(content)
	return "blob:" + hex.EncodeToString(h.Sum(nil))
}

// NewTapObserver wraps a tap so that publishing to it is read-only and reports
// what it would have written.
func NewTapObserver(api brew.FileAPI) *TapObserver {
	return &TapObserver{api: api}
}
