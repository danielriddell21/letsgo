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

	if err := api.WriteFile(ctx, tap, github.FileInput{
		Path:    path,
		Message: fmt.Sprintf("%s %s", f.Name, f.Version),
		Content: content,
		SHA:     sha,
	}); err != nil {
		return Result{}, fmt.Errorf("brew: publishing %s to %s: %w", path, tap, err)
	}
	return Result{Path: path, Status: status}, nil
}
