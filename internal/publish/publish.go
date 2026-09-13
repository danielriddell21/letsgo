// Package publish uploads a built release, idempotently.
//
// Every step is keyed by content, so running it twice is safe and the second
// run does only what the first did not finish. That property is not
// bookkeeping: because artifacts are reproducible, "is this asset already
// correct?" is a question that can be answered by comparing digests rather
// than by trusting a record of what happened last time.
//
// There is consequently no state file. A local record of completed uploads
// would be a cache of an answer the server can give directly, and a cache that
// disagrees with the server is worse than no cache.
package publish

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/danielriddell21/letsgo/internal/publish/github"
)

// Forge is the part of a release API that publishing needs.
//
// An interface rather than a concrete client so that --snapshot can run the
// same code down to the last decision and swap only the final write. A dry run
// that takes a different path proves less than it appears to.
type Forge interface {
	ReleaseByTag(ctx context.Context, repo github.Repo, tag string) (*github.Release, error)
	CreateRelease(ctx context.Context, repo github.Repo, in github.ReleaseInput) (*github.Release, error)
	UpdateRelease(ctx context.Context, repo github.Repo, id int64, in github.ReleaseInput) (*github.Release, error)
	Assets(ctx context.Context, repo github.Repo, releaseID int64) ([]github.Asset, error)
	DeleteAsset(ctx context.Context, repo github.Repo, assetID int64) error
	UploadAsset(ctx context.Context, repo github.Repo, releaseID int64, name string, size int64, content io.Reader) (*github.Asset, error)
}

// Options describe a publication.
type Options struct {
	Client Forge
	Repo   github.Repo

	// Dir holds the files named in Files.
	Dir   string
	Files []string

	// Sums maps each filename to its expected SHA-256. Uploads are verified
	// against these rather than against whatever happens to be on disk.
	Sums map[string]string

	Release github.ReleaseInput

	// Notes controls what happens to the description of a release that
	// already exists. The zero value replaces it.
	Notes NotesMode

	// Logf reports progress. Optional.
	Logf func(format string, args ...any)
}

// Result records what happened.
type Result struct {
	Release  *github.Release
	Uploaded []string
	Skipped  []string
	Replaced []string
	Created  bool

	// AppendedNotes records that the generated notes were added after notes
	// that were already there.
	AppendedNotes bool

	// NotesRefused records that the forge would not let us set the
	// description, so the release carries whatever it had before.
	NotesRefused bool
}

// NotesMode says what to do with the description of a release that already
// exists.
type NotesMode string

const (
	// NotesReplace overwrites it. This is the default: a release letsgo
	// publishes should describe what letsgo built, and a re-run after a
	// corrected changelog should carry the correction through.
	NotesReplace NotesMode = ""

	// NotesAppend adds the generated notes after whatever is already there,
	// for releases whose description is written by hand and then topped up
	// with the generated changelog.
	NotesAppend NotesMode = "append"
)

// Run creates or updates the release and uploads every file that is not
// already present and correct.
func Run(ctx context.Context, o Options) (*Result, error) {
	logf := o.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	result := &Result{}

	if err := ensureRelease(ctx, o, result, logf); err != nil {
		return nil, err
	}

	assets, err := o.Client.Assets(ctx, o.Repo, result.Release.ID)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]github.Asset, len(assets))
	for _, a := range assets {
		byName[a.Name] = a
	}

	for _, name := range o.Files {
		path := filepath.Join(o.Dir, name)
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("publish: %w", err)
		}

		if existingAsset, found := byName[name]; found {
			switch matches(existingAsset, o.Sums[name], info.Size()) {
			case match:
				result.Skipped = append(result.Skipped, name)
				continue
			case mismatch:
				// A partial upload leaves an asset that looks present and is
				// not usable. Replacing it is the only way to converge.
				if err := o.Client.DeleteAsset(ctx, o.Repo, existingAsset.ID); err != nil {
					return nil, err
				}
				result.Replaced = append(result.Replaced, name)
				logf("replacing %s (does not match the built artifact)", name)
			}
		}

		if err := upload(ctx, o, result.Release.ID, name, path, info.Size()); err != nil {
			return nil, err
		}
		result.Uploaded = append(result.Uploaded, name)
	}

	return result, nil
}

// isRefusal reports whether the forge declined on grounds of permission
// rather than failing. A refusal is a fact about what this token may do; an
// error is a fact about whether the call worked.
func isRefusal(err error) bool {
	var apiErr *github.APIError
	return errors.As(err, &apiErr) &&
		(apiErr.StatusCode == http.StatusForbidden || apiErr.StatusCode == http.StatusUnauthorized)
}

type verdict int

const (
	match verdict = iota
	mismatch
)

// matches decides whether an already-uploaded asset is the file we built.
//
// A digest is conclusive. Where the forge does not report one, size is the
// only signal available: it catches the truncated upload that resume exists
// for, and cannot catch a same-size difference. That limit is real, and
// preferable to assuming any asset with the right name is correct.
func matches(asset github.Asset, wantSHA256 string, size int64) verdict {
	if remote, ok := asset.SHA256(); ok && wantSHA256 != "" {
		if remote == wantSHA256 {
			return match
		}
		return mismatch
	}
	if asset.Size == size && size > 0 {
		return match
	}
	return mismatch
}

// ensureRelease creates the release, or adopts the one already there.
//
// An existing release is the expected state on a re-run rather than a
// conflict, which is what makes the whole operation resumable.
func ensureRelease(ctx context.Context, o Options, result *Result, logf func(string, ...any)) error {
	existing, err := o.Client.ReleaseByTag(ctx, o.Repo, o.Release.TagName)
	if err != nil {
		return err
	}

	if existing == nil {
		created, err := o.Client.CreateRelease(ctx, o.Repo, o.Release)
		if err != nil {
			return err
		}
		result.Release, result.Created = created, true
		logf("created release %s", o.Release.TagName)
		return nil
	}

	input := o.Release
	if o.Notes == NotesAppend && strings.TrimSpace(existing.Body) != "" {
		input.Body = strings.TrimRight(existing.Body, "\n") + "\n\n" + o.Release.Body
		result.AppendedNotes = true
	}

	updated, err := o.Client.UpdateRelease(ctx, o.Repo, existing.ID, input)
	switch {
	case err == nil:
		result.Release = updated
		logf("release %s already exists; resuming", o.Release.TagName)

	case isRefusal(err):
		// The description could not be set, but the assets are the substance
		// of a release. Abandoning an upload we are permitted to perform,
		// because of a description we are not, would leave the release
		// emptier than doing the part that is allowed.
		result.Release = existing
		result.NotesRefused = true
		logf("release %s already exists, and its notes cannot be edited with this token", o.Release.TagName)
		logf("continuing with the assets; the existing description stands")

	default:
		return err
	}
	return nil
}

func upload(ctx context.Context, o Options, releaseID int64, name, path string, size int64) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	defer func() { _ = f.Close() }()

	asset, err := o.Client.UploadAsset(ctx, o.Repo, releaseID, name, size, f)
	if err != nil {
		return err
	}

	// Verify rather than assume. An upload that reports success but stored
	// something else would otherwise become a release nobody can verify, and
	// the digest is right there in the response.
	if want := o.Sums[name]; want != "" {
		if got, ok := asset.SHA256(); ok && got != want {
			return fmt.Errorf("publish: %s uploaded but the forge reports digest %s, expected %s",
				name, got, want)
		}
	}
	return nil
}
