package brew

import (
	"bytes"
	"context"
	"fmt"

	"github.com/danielriddell21/letsgo/internal/publish/github"
)

// FileAPI is the part of a forge that publishing a formula needs.
//
// An interface for the same reason the release path has one: --snapshot must
// reach every decision the real run reaches, including "this formula is
// already correct", and swap only the write.
type FileAPI interface {
	ReadFile(ctx context.Context, repo github.Repo, path string) (*github.File, error)
	WriteFile(ctx context.Context, repo github.Repo, in github.FileInput) error
}

// Status says what publishing did.
type Status string

const (
	Created   Status = "created"
	Updated   Status = "updated"
	Unchanged Status = "unchanged"
)

// Result records one formula's publication.
type Result struct {
	Path   string
	Status Status
}

// Author is the identity a formula is published under.
//
// Fixed rather than taken from the token: a tap's history should say that
// letsgo wrote the entry, and that must not change because a repository
// publishes with a credential of its own. It is a variable so that a
// repository can override it, and so a test can assert what was sent.
//
// This names the letsgo-champ App, which is what publishes the formula. The
// number is the App's bot user id, not its App id: the address GitHub resolves
// to an account is "<bot user id>+<login>@users.noreply.github.com", and the
// two are different namespaces — the App id there would look right and link to
// nothing.
var Author = github.Committer{
	Name:  "letsgo-champ[bot]",
	Email: "293666020+letsgo-champ[bot]@users.noreply.github.com",
}

// Publish writes the formula to the tap, unless it is already exactly right.
//
// Skipping an identical file is not an optimisation. A release that is re-run
// — after a failed upload, or because the notes changed — would otherwise
// leave a commit in someone else's repository saying nothing happened.
func Publish(ctx context.Context, api FileAPI, tap github.Repo, f Formula) (Result, error) {
	content, err := f.Render()
	if err != nil {
		return Result{}, err
	}
	path := f.FileName()

	existing, err := api.ReadFile(ctx, tap, path)
	if err != nil {
		return Result{}, err
	}

	status, sha := Created, ""
	if existing != nil {
		if bytes.Equal(existing.Content, content) {
			return Result{Path: path, Status: Unchanged}, nil
		}
		status, sha = Updated, existing.SHA
	}

	author := Author
	if err := api.WriteFile(ctx, tap, github.FileInput{
		Path:    path,
		Message: fmt.Sprintf("%s %s", f.Name, f.Version),
		Content: content,
		SHA:     sha,
		Author:  &author,
	}); err != nil {
		return Result{}, fmt.Errorf("brew: publishing %s to %s: %w", path, tap, err)
	}
	return Result{Path: path, Status: status}, nil
}
