package discover

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNewScope(t *testing.T) {
	top := t.TempDir()

	t.Run("a root module has no scope", func(t *testing.T) {
		s, err := NewScope(top, top)
		if err != nil {
			t.Fatal(err)
		}
		if s != (Scope{}) {
			t.Errorf("Scope = %+v, want empty", s)
		}
	})

	t.Run("a nested module carries its own directory as prefix", func(t *testing.T) {
		nested := filepath.Join(top, "services", "api")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		s, err := NewScope(top, nested)
		if err != nil {
			t.Fatal(err)
		}
		if s.Dir != "services/api" || s.Prefix != "services/api/" {
			t.Errorf("Scope = %+v", s)
		}
	})

	t.Run("a directory outside the repository is refused", func(t *testing.T) {
		if _, err := NewScope(top, t.TempDir()); err == nil {
			t.Error("expected an error for a module outside the repository")
		}
	})

	// git's own --show-toplevel already resolves symlinks (macOS's
	// /tmp -> /private/tmp, among others), so a caller comparing against its
	// own, unresolved directory must not conclude a module is outside its own
	// repository merely because the two spellings disagree.
	t.Run("a symlinked top level still matches the module inside it", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("creating a symlink needs elevated privileges on Windows")
		}
		actual := filepath.Join(t.TempDir(), "real")
		if err := os.Mkdir(actual, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(actual, link); err != nil {
			t.Fatal(err)
		}
		nested := filepath.Join(link, "services", "api")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}

		s, err := NewScope(link, nested)
		if err != nil {
			t.Fatal(err)
		}
		if s.Dir != "services/api" {
			t.Errorf("Scope = %+v", s)
		}
	})
}

func TestScopeMatchesTag(t *testing.T) {
	cases := []struct {
		name      string
		prefix    string
		tag       string
		wantRest  string
		wantMatch bool
	}{
		{"root scope accepts a plain version", "", "v1.2.3", "v1.2.3", true},
		{"root scope rejects another scope's tag", "", "services/api/v1.0.0", "", false},
		{"nested scope accepts its own tag", "services/api/", "services/api/v1.0.0", "v1.0.0", true},
		{"nested scope rejects the root's tag", "services/api/", "v1.0.0", "", false},
		{"nested scope rejects a sibling's tag", "services/api/", "services/web/v1.0.0", "", false},
		{"a prefix match without a version is not a tag", "services/api/", "services/api/latest", "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			scope := Scope{Prefix: c.prefix}
			rest, ok := scope.MatchesTag(c.tag)
			if ok != c.wantMatch || rest != c.wantRest {
				t.Errorf("MatchesTag(%q) = %q, %v, want %q, %v", c.tag, rest, ok, c.wantRest, c.wantMatch)
			}
		})
	}
}

func TestScopeLatestTag(t *testing.T) {
	cases := []struct {
		name    string
		prefix  string
		tags    []string
		wantTag string
		wantOK  bool
	}{
		{
			"root scope picks the highest version", "",
			[]string{"v1.0.0", "v2.0.0", "v1.5.0"},
			"v2.0.0", true,
		},
		{
			"nested scope ignores other scopes and the root", "services/api/",
			[]string{"v9.9.9", "services/web/v8.0.0", "services/api/v1.0.0", "services/api/v1.2.3"},
			"services/api/v1.2.3", true,
		},
		{
			"no matching tag has no releases", "services/api/",
			[]string{"v1.0.0", "services/web/v1.0.0"},
			"", false,
		},
		{"no tags at all has no releases", "", nil, "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			scope := Scope{Prefix: c.prefix}
			tag, ok := scope.LatestTag(c.tags)
			if ok != c.wantOK || tag != c.wantTag {
				t.Errorf("LatestTag(%v) = %q, %v, want %q, %v", c.tags, tag, ok, c.wantTag, c.wantOK)
			}
		})
	}
}

func TestScopePreviousTag(t *testing.T) {
	cases := []struct {
		name    string
		prefix  string
		tags    []string
		tag     string
		wantTag string
		wantOK  bool
	}{
		{
			"a stable release's previous is the highest stable below it, skipping an rc", "services/api/",
			[]string{"services/api/v1.0.0", "services/api/v1.1.0-rc.1", "services/web/v1.0.5", "services/api/v1.1.0"},
			"services/api/v1.1.0",
			"services/api/v1.0.0", true,
		},
		{
			"a prerelease's previous is the highest release of any kind below it", "",
			[]string{"v1.0.0", "v1.1.0-rc.1"},
			"v1.1.0-rc.2",
			"v1.1.0-rc.1", true,
		},
		{
			"a first release has no previous", "",
			[]string{},
			"v1.0.0",
			"", false,
		},
		{
			"a tag outside the scope has no previous", "services/api/",
			[]string{"services/api/v1.0.0"},
			"v2.0.0",
			"", false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			scope := Scope{Prefix: c.prefix}
			tag, ok := scope.PreviousTag(c.tags, c.tag)
			if ok != c.wantOK || tag != c.wantTag {
				t.Errorf("PreviousTag(%v, %q) = %q, %v, want %q, %v", c.tags, c.tag, tag, ok, c.wantTag, c.wantOK)
			}
		})
	}
}

func TestScopeLatestStableTag(t *testing.T) {
	cases := []struct {
		name    string
		prefix  string
		tags    []string
		exclude []string
		wantTag string
		wantOK  bool
	}{
		{
			"picks the highest stable, skipping a newer rc", "services/api/",
			[]string{"services/api/v1.0.0", "services/api/v1.1.0-rc.1", "services/web/v9.0.0"},
			nil,
			"services/api/v1.0.0", true,
		},
		{
			"excludes tags pointing at HEAD", "",
			[]string{"v1.0.0", "v1.1.0"},
			[]string{"v1.1.0"},
			"v1.0.0", true,
		},
		{
			"no stable release has no answer", "",
			[]string{"v1.0.0-rc.1"},
			nil,
			"", false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			scope := Scope{Prefix: c.prefix}
			tag, ok := scope.LatestStableTag(c.tags, c.exclude...)
			if ok != c.wantOK || tag != c.wantTag {
				t.Errorf("LatestStableTag(%v, %v) = %q, %v, want %q, %v", c.tags, c.exclude, tag, ok, c.wantTag, c.wantOK)
			}
		})
	}
}
