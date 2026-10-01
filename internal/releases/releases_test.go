package releases_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"testing"

	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/manifest"
	"github.com/danielriddell21/letsgo/internal/releases"
	"github.com/danielriddell21/letsgo/internal/semver"
)

// fake is an in-memory forge. It mirrors the forge's rules that callers lean
// on: by-tag and latest hide drafts, latest also hides prereleases, and the
// list shows everything.
type fake struct {
	all       []releases.Published
	tags      []string
	tagsErr   error
	listErr   error
	downloads map[int64][]byte
	asked     []string
}

func (f *fake) ReleaseByTag(_ context.Context, tag string) (*releases.Published, error) {
	f.asked = append(f.asked, "by-tag "+tag)
	for i := range f.all {
		if f.all[i].Tag == tag && !f.all[i].Draft {
			return &f.all[i], nil
		}
	}
	return nil, nil
}

func (f *fake) LatestRelease(context.Context) (*releases.Published, error) {
	f.asked = append(f.asked, "latest")
	for i := range f.all {
		if !f.all[i].Draft && !f.all[i].Prerelease {
			return &f.all[i], nil
		}
	}
	return nil, nil
}

func (f *fake) Tags(context.Context, int) ([]string, error) {
	f.asked = append(f.asked, "tags")
	return f.tags, f.tagsErr
}

func (f *fake) ListReleases(context.Context) ([]releases.Published, error) {
	return f.all, f.listErr
}

func (f *fake) DownloadAsset(_ context.Context, a releases.Asset) ([]byte, error) {
	return f.downloads[a.ID], nil
}

func rel(tag string, opts ...func(*releases.Published)) releases.Published {
	p := releases.Published{Tag: tag}
	for _, o := range opts {
		o(&p)
	}
	return p
}

func draft(p *releases.Published) { p.Draft = true }
func pre(p *releases.Published)   { p.Prerelease = true }
func yanked(p *releases.Published) {
	p.Body = "> [!CAUTION]\n> **This release is retracted.**"
}

func TestByTagHidesDrafts(t *testing.T) {
	f := &fake{all: []releases.Published{rel("v1.0.0"), rel("v2.0.0-rc.1", draft)}}

	got, err := releases.ByTag(t.Context(), f, "v1.0.0")
	if err != nil || got == nil || got.Tag != "v1.0.0" {
		t.Fatalf("ByTag(v1.0.0) = %v, %v", got, err)
	}
	got, err = releases.ByTag(t.Context(), f, "v2.0.0-rc.1")
	if err != nil || got != nil {
		t.Errorf("ByTag(draft) = %v, %v; want nil, nil", got, err)
	}
}

func TestByTagIncludingDraftsSeesDrafts(t *testing.T) {
	f := &fake{all: []releases.Published{rel("v1.0.0"), rel("v2.0.0-rc.1", draft)}}

	tests := []struct {
		name string
		tag  string
		want bool
	}{
		{"a published release", "v1.0.0", true},
		{"a draft release candidate", "v2.0.0-rc.1", true},
		{"a tag never released", "v9.9.9", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := releases.ByTagIncludingDrafts(t.Context(), f, tt.tag)
			if err != nil {
				t.Fatal(err)
			}
			if (got != nil) != tt.want {
				t.Errorf("found = %v, want %v", got != nil, tt.want)
			}
		})
	}
}

func TestByTagIncludingDraftsSurfacesAListError(t *testing.T) {
	want := errors.New("boom")
	_, err := releases.ByTagIncludingDrafts(t.Context(), &fake{listErr: want}, "v1.0.0")
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}

func TestLatest(t *testing.T) {
	all := []releases.Published{
		rel("v1.5.0-rc.1", pre),
		rel("api/v2.0.0-rc.1", pre),
		rel("api/v1.9.0"),
		rel("api/v2.0.0-rc.0", pre, yanked),
		rel("v1.4.0"),
	}
	tags := []string{"v1.4.0", "api/v1.9.0", "api/v2.0.0-rc.1", "api/v2.0.0-rc.0", "v1.5.0-rc.1"}

	tests := []struct {
		name  string
		scope discover.Scope
		want  string // "" means nil
		asked []string
	}{
		{
			// The forge's own latest: skips prereleases, ignores scopes.
			name:  "unscoped uses the forge's latest",
			want:  "api/v1.9.0",
			asked: []string{"latest"},
		},
		{
			// Characterised, not endorsed: a scoped lookup picks the highest
			// version in scope, prereleases included (#164).
			name:  "scoped picks the highest version in scope, prereleases included",
			scope: discover.Scope{Dir: "api", Prefix: "api/"},
			want:  "api/v2.0.0-rc.1",
			asked: []string{"tags", "by-tag api/v2.0.0-rc.1"},
		},
		{
			name:  "scoped with no tag in scope has no release",
			scope: discover.Scope{Dir: "cli", Prefix: "cli/"},
			want:  "",
			asked: []string{"tags"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fake{all: all, tags: tags}
			got, err := releases.Latest(t.Context(), f, tt.scope)
			if err != nil {
				t.Fatal(err)
			}
			if tag := tagOf(got); tag != tt.want {
				t.Errorf("tag = %q, want %q", tag, tt.want)
			}
			if !slices.Equal(f.asked, tt.asked) {
				t.Errorf("asked %v, want %v", f.asked, tt.asked)
			}
		})
	}
}

func TestLatestSurfacesATagsError(t *testing.T) {
	want := errors.New("boom")
	f := &fake{tagsErr: want}
	_, err := releases.Latest(t.Context(), f, discover.Scope{Dir: "api", Prefix: "api/"})
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}

func TestPerMajor(t *testing.T) {
	all := []releases.Published{
		rel("v1.0.0"),
		rel("v1.2.0"),
		rel("v1.3.0", yanked), // retracted: v1.2.0 is the newest v1
		rel("v2.0.0"),
		rel("v2.1.0", draft),    // draft
		rel("v2.0.1-rc.1"),      // prerelease by its version, as audit decides it
		rel("v3.0.0-rc.1", pre), // v3 has no stable release
		rel("v10.0.0"),          // numeric, not lexical, major order
		rel("api/v1.9.0"),       // out of scope
		rel("not-a-version"),
	}

	got, err := releases.PerMajor(t.Context(), &fake{all: all}, discover.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	var tags []string
	for _, p := range got {
		tags = append(tags, p.Tag)
	}
	want := []string{"v1.2.0", "v2.0.0", "v10.0.0"}
	if !slices.Equal(tags, want) {
		t.Errorf("PerMajor = %v, want %v", tags, want)
	}
}

func TestPerMajorIsScoped(t *testing.T) {
	all := []releases.Published{rel("v1.0.0"), rel("api/v1.9.0"), rel("api/v2.0.0")}

	got, err := releases.PerMajor(t.Context(), &fake{all: all}, discover.Scope{Dir: "api", Prefix: "api/"})
	if err != nil {
		t.Fatal(err)
	}
	var tags []string
	for _, p := range got {
		tags = append(tags, p.Tag)
	}
	if want := []string{"api/v1.9.0", "api/v2.0.0"}; !slices.Equal(tags, want) {
		t.Errorf("PerMajor = %v, want %v", tags, want)
	}
}

func TestManifest(t *testing.T) {
	good := []byte(`{"schema":1}`)
	if _, err := manifest.Decode(good); err != nil {
		t.Skipf("fixture is not a valid manifest here: %v", err)
	}

	t.Run("a release without a manifest says so by type", func(t *testing.T) {
		_, _, err := releases.Manifest(t.Context(), &fake{}, &releases.Published{Tag: "v1.0.0"})
		var e *releases.NoManifestError
		if !errors.As(err, &e) || e.Tag != "v1.0.0" {
			t.Fatalf("err = %v, want *NoManifestError for v1.0.0", err)
		}
		if !releases.IsNoManifest(err) {
			t.Error("IsNoManifest = false")
		}
	})

	t.Run("a bad manifest names the release", func(t *testing.T) {
		f := &fake{downloads: map[int64][]byte{7: []byte("not json")}}
		p := &releases.Published{Tag: "v1.0.0", Assets: []releases.Asset{{ID: 7, Name: manifest.FileName}}}
		_, _, err := releases.Manifest(t.Context(), f, p)
		if err == nil || releases.IsNoManifest(err) {
			t.Fatalf("err = %v, want a decode error", err)
		}
	})

	t.Run("the digest is of the bytes as published", func(t *testing.T) {
		f := &fake{downloads: map[int64][]byte{7: good}}
		p := &releases.Published{Tag: "v1.0.0", Assets: []releases.Asset{{ID: 7, Name: manifest.FileName}}}
		m, sum, err := releases.Manifest(t.Context(), f, p)
		if err != nil || m == nil {
			t.Fatalf("Manifest = %v, %v", m, err)
		}
		if want := sha256.Sum256(good); string(sum) != string(want[:]) {
			t.Errorf("sum = %x, want %x", sum, want)
		}
	})
}

func TestIsRetracted(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"the notice yank prepends", "> [!CAUTION]\n> retracted", true},
		{"notes that merely mention it", "see > [!CAUTION] later", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := releases.IsRetracted(tt.body); got != tt.want {
				t.Errorf("IsRetracted = %v, want %v", got, tt.want)
			}
		})
	}
}

func tagOf(p *releases.Published) string {
	if p == nil {
		return ""
	}
	return p.Tag
}

func TestBest(t *testing.T) {
	all := []releases.Published{
		rel("api/v1.0.0"),
		rel("api/v1.2.0-beta.1"),
		rel("api/v1.1.0", yanked),
		rel("api/v1.3.0", draft),
		rel("v9.0.0"), // out of scope
		rel("api/not-a-version"),
	}
	scope := discover.Scope{Dir: "api", Prefix: "api/"}

	got, err := releases.Best(t.Context(), &fake{all: all}, scope, func(semver.Version) bool { return true })
	if err != nil || got == nil || got.Tag != "api/v1.2.0-beta.1" {
		t.Fatalf("Best(any) = %v, %v, want the beta", got, err)
	}

	got, err = releases.Best(t.Context(), &fake{all: all}, scope, func(v semver.Version) bool { return !v.IsPrerelease() })
	if err != nil || got == nil || got.Tag != "api/v1.0.0" {
		t.Fatalf("Best(stable) = %v, %v, want v1.0.0", got, err)
	}

	got, err = releases.Best(t.Context(), &fake{all: all}, scope, func(semver.Version) bool { return false })
	if err != nil || got != nil {
		t.Fatalf("Best(none) = %v, %v, want nil", got, err)
	}

	if _, err := releases.Best(t.Context(), &fake{listErr: errors.New("boom")}, scope, nil); err == nil {
		t.Fatal("a listing error was swallowed")
	}
}
