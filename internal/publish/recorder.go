package publish

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/danielriddell21/letsgo/internal/bytesize"
	"github.com/danielriddell21/letsgo/internal/publish/github"
)

// Recorder is a Forge that writes down what would have happened instead of
// doing it.
//
// It exists so that --snapshot is a rehearsal rather than a different
// procedure. Every decision the real run makes — which assets already exist,
// which would be replaced, whether the notes would be overwritten — is made
// here too, against the same code. Only the final write is swapped.
//
// A dry run that takes its own path can only tell you that the dry run works.
type Recorder struct {
	// Existing is the release the run should believe already exists. Nil means
	// the release would be created.
	Existing *github.Release

	// Files are repository files the run should believe are already there,
	// keyed by path. A rehearsal of a Homebrew formula that is already correct
	// should report that, not a write.
	Files map[string][]byte

	out    io.Writer
	nextID int64
	Calls  []string
}

// NewRecorder returns a Forge that reports to out.
func NewRecorder(out io.Writer) *Recorder {
	return &Recorder{out: out, nextID: 1}
}

func (r *Recorder) record(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	r.Calls = append(r.Calls, line)
	if r.out != nil {
		fmt.Fprintln(r.out, "    "+line)
	}
}

func (r *Recorder) ReleaseByTag(_ context.Context, repo github.Repo, tag string) (*github.Release, error) {
	if r.Existing == nil {
		r.record("GET  release %s on %s -> not found", tag, repo)
		return nil, nil
	}
	r.record("GET  release %s on %s -> exists (%d assets)", tag, repo, len(r.Existing.Assets))
	return r.Existing, nil
}

func (r *Recorder) CreateRelease(_ context.Context, repo github.Repo, in github.ReleaseInput) (*github.Release, error) {
	r.record("POST create release %s on %s%s", in.TagName, repo, flags(in))
	r.record("     notes: %s", summarise(in.Body))
	return &github.Release{
		ID: 1, TagName: in.TagName, Name: in.Name, Body: in.Body,
		Draft: in.Draft, Prerelease: in.Prerelease,
		HTMLURL: fmt.Sprintf("https://github.com/%s/releases/tag/%s (not created)", repo, in.TagName),
	}, nil
}

func (r *Recorder) UpdateRelease(_ context.Context, repo github.Repo, id int64, in github.ReleaseInput) (*github.Release, error) {
	r.record("PATCH release %d on %s%s", id, repo, flags(in))
	r.record("     notes: %s", summarise(in.Body))
	existing := *r.Existing
	existing.Body, existing.Name = in.Body, in.Name
	return &existing, nil
}

func (r *Recorder) Assets(_ context.Context, _ github.Repo, releaseID int64) ([]github.Asset, error) {
	if r.Existing == nil {
		return nil, nil
	}
	r.record("GET  assets of release %d -> %d", releaseID, len(r.Existing.Assets))
	return r.Existing.Assets, nil
}

func (r *Recorder) DeleteAsset(_ context.Context, _ github.Repo, assetID int64) error {
	r.record("DELETE asset %d", assetID)
	return nil
}

func (r *Recorder) UploadAsset(_ context.Context, _ github.Repo, releaseID int64, name string, size int64, content io.Reader) (*github.Asset, error) {
	// Read and discard so that an unreadable file fails here exactly as it
	// would in a real run.
	n, err := io.Copy(io.Discard, content)
	if err != nil {
		return nil, fmt.Errorf("publish: reading %s: %w", name, err)
	}
	if n != size {
		return nil, fmt.Errorf("publish: %s declared %d bytes but yielded %d", name, size, n)
	}

	r.record("POST upload %s (%s)", name, bytesize.Size(size))
	r.nextID++
	return &github.Asset{ID: r.nextID, Name: name, Size: size}, nil
}

func flags(in github.ReleaseInput) string {
	var set []string
	if in.Draft {
		set = append(set, "draft")
	}
	if in.Prerelease {
		set = append(set, "prerelease")
	}
	if len(set) == 0 {
		return ""
	}
	return " [" + strings.Join(set, ", ") + "]"
}

func summarise(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return "(none)"
	}
	lines := strings.Split(body, "\n")
	first := strings.TrimSpace(lines[0])
	if len(lines) == 1 {
		return first
	}
	return fmt.Sprintf("%s … (%d lines)", first, len(lines))
}

func (r *Recorder) ReadFile(_ context.Context, repo github.Repo, path string) (*github.File, error) {
	content, ok := r.Files[path]
	if !ok {
		r.record("GET  %s/%s -> not found", repo, path)
		return nil, nil
	}
	r.record("GET  %s/%s -> %s", repo, path, bytesize.Size(len(content)))
	return &github.File{Path: path, SHA: "recorded", Content: content}, nil
}

func (r *Recorder) WriteFile(_ context.Context, repo github.Repo, in github.FileInput) error {
	verb := "create"
	if in.SHA != "" {
		verb = "update"
	}
	r.record("PUT  %s %s/%s (%s) %q", verb, repo, in.Path, bytesize.Size(len(in.Content)), in.Message)
	return nil
}
