package brew_test

import (
	"context"
	"errors"
	"testing"

	"github.com/danielriddell21/letsgo/internal/brew"
	"github.com/danielriddell21/letsgo/internal/publish/github"
)

// fakeTap is a repository that remembers what was written to it.
type fakeTap struct {
	files  map[string][]byte
	writes []github.FileInput
	err    error
}

func (f *fakeTap) ReadFile(_ context.Context, _ github.Repo, path string) (*github.File, error) {
	if f.err != nil {
		return nil, f.err
	}
	content, ok := f.files[path]
	if !ok {
		return nil, nil
	}
	return &github.File{Path: path, SHA: "blob-" + path, Content: content}, nil
}

func (f *fakeTap) WriteFile(_ context.Context, _ github.Repo, in github.FileInput) error {
	f.writes = append(f.writes, in)
	if f.files == nil {
		f.files = map[string][]byte{}
	}
	f.files[in.Path] = in.Content
	return nil
}

func TestPublishCreatesThenSkips(t *testing.T) {
	tap := &fakeTap{}
	repo := github.Repo{Owner: "you", Name: "homebrew-tap"}
	ctx := context.Background()

	first, err := brew.Publish(ctx, tap, repo, sample())
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != brew.Created || first.Path != "Formula/my-tool.rb" {
		t.Fatalf("first publish = %+v", first)
	}
	if len(tap.writes) != 1 {
		t.Fatalf("want one write, got %d", len(tap.writes))
	}
	// A create must not claim to replace a blob that does not exist.
	if tap.writes[0].SHA != "" {
		t.Errorf("create carried a blob sha: %q", tap.writes[0].SHA)
	}

	// Re-running a release must not commit to someone else's repository to
	// say that nothing changed.
	second, err := brew.Publish(ctx, tap, repo, sample())
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != brew.Unchanged {
		t.Errorf("second publish = %+v, want unchanged", second)
	}
	if len(tap.writes) != 1 {
		t.Errorf("an unchanged formula was written again: %d writes", len(tap.writes))
	}
}

func TestPublishUpdatesWithTheBlobItRead(t *testing.T) {
	tap := &fakeTap{files: map[string][]byte{"Formula/my-tool.rb": []byte("old\n")}}
	repo := github.Repo{Owner: "you", Name: "homebrew-tap"}

	result, err := brew.Publish(context.Background(), tap, repo, sample())
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != brew.Updated {
		t.Fatalf("result = %+v, want updated", result)
	}
	// Without the blob sha GitHub refuses the write, and with a stale one it
	// refuses rather than clobbering a change made in between.
	if tap.writes[0].SHA != "blob-Formula/my-tool.rb" {
		t.Errorf("update carried sha %q", tap.writes[0].SHA)
	}
	if tap.writes[0].Message != "my-tool 1.2.3" {
		t.Errorf("commit message = %q", tap.writes[0].Message)
	}
}

func TestPublishReportsAForgeFailure(t *testing.T) {
	tap := &fakeTap{err: errors.New("no")}
	_, err := brew.Publish(context.Background(), tap,
		github.Repo{Owner: "you", Name: "homebrew-tap"}, sample())
	if err == nil {
		t.Fatal("want an error")
	}
}
