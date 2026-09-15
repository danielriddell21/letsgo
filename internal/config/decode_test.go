package config

import "testing"

func TestImageDirective(t *testing.T) {
	cfg := decode(t, "image\n")
	if cfg.Image == nil || cfg.Image.Reference != "" || cfg.Image.Base != "" {
		t.Fatalf("bare image = %+v", cfg.Image)
	}

	cfg = decode(t, "image ghcr.io/you/tool\nimage base gcr.io/distroless/static:nonroot\n")
	if cfg.Image == nil {
		t.Fatal("image = nil")
	}
	if cfg.Image.Reference != "ghcr.io/you/tool" {
		t.Errorf("reference = %q", cfg.Image.Reference)
	}
	if cfg.Image.Base != "gcr.io/distroless/static:nonroot" {
		t.Errorf("base = %q", cfg.Image.Base)
	}

	cfg = decode(t, "image (\n\tghcr.io/you/tool\n\tbase gcr.io/distroless/static\n)\n")
	if cfg.Image == nil || cfg.Image.Reference != "ghcr.io/you/tool" ||
		cfg.Image.Base != "gcr.io/distroless/static" {
		t.Errorf("block form = %+v", cfg.Image)
	}
}

func TestImageCmdAndExpose(t *testing.T) {
	cfg := decode(t, "image (\n\tghcr.io/you/tool\n\tcmd serve --addr :8080\n\texpose 8080 53/udp\n)\n")
	if cfg.Image == nil {
		t.Fatal("image = nil")
	}
	if got := cfg.Image.Cmd; len(got) != 3 || got[0] != "serve" || got[2] != ":8080" {
		t.Errorf("cmd = %q", got)
	}
	// Bare ports gain the default protocol, so the image config carries the
	// spec's "port/proto" form either way.
	if got := cfg.Image.Expose; len(got) != 2 || got[0] != "8080/tcp" || got[1] != "53/udp" {
		t.Errorf("expose = %q", got)
	}
}

// Two references would leave one of them silently ignored.
func TestImageDirectiveRejectsRepeats(t *testing.T) {
	for _, in := range []string{
		"image a/b\nimage c/d\n",
		"image base a/b\nimage base c/d\n",
		"image base\n",
		"image a b c\n",
		"image cmd\n",
		"image cmd serve\nimage cmd other\n",
		"image expose\n",
		"image expose 0\n",
		"image expose 70000\n",
		"image expose http\n",
		"image expose 8080/sctp\n",
	} {
		if _, err := Decode(parse(t, in)); err == nil {
			t.Errorf("%q should not have parsed", in)
		}
	}
}

// No directive means no image: publishing a package to a registry is not
// something to do to a repository that did not ask.
func TestNoImageDirectiveMeansNoImage(t *testing.T) {
	if cfg := decode(t, "project foo\n"); cfg.Image != nil {
		t.Errorf("image = %+v, want nil", cfg.Image)
	}
}

func TestModuleDirective(t *testing.T) {
	if got := decode(t, "module web\n").ModuleDir; got != "web" {
		t.Errorf("ModuleDir = %q", got)
	}
	if got := decode(t, "module ./web/\n").ModuleDir; got != "web" {
		t.Errorf("ModuleDir = %q, want the cleaned path", got)
	}
	// Naming the root is what every repository already means.
	if got := decode(t, "module .\n").ModuleDir; got != "" {
		t.Errorf("ModuleDir = %q, want empty", got)
	}
}

// The path is joined to the repository root and handed to the toolchain, so
// one that escapes would build something the commit does not contain.
func TestModuleDirectiveRejectsEscapes(t *testing.T) {
	for _, in := range []string{
		"module /etc\n",
		"module ..\n",
		"module ../sibling\n",
		"module web extra\n",
		"module\n",
		"module a\nmodule b\n",
	} {
		if _, err := Decode(parse(t, in)); err == nil {
			t.Errorf("%q should not have parsed", in)
		}
	}
}

func TestVersionDirective(t *testing.T) {
	cfg := decode(t, "version internal/buildinfo.Version\nversion commit internal/buildinfo.Commit\n")
	if cfg.Version == nil {
		t.Fatal("Version = nil")
	}
	if cfg.Version.Version != "internal/buildinfo.Version" {
		t.Errorf("Version = %q", cfg.Version.Version)
	}
	if cfg.Version.Commit != "internal/buildinfo.Commit" {
		t.Errorf("Commit = %q", cfg.Version.Commit)
	}
	if cfg.Version.Date != "" {
		t.Errorf("Date = %q, want empty", cfg.Version.Date)
	}

	// The fully qualified form is the same thing written out.
	cfg = decode(t, "version github.com/you/tool/internal/buildinfo.Version\n")
	if cfg.Version.Version != "github.com/you/tool/internal/buildinfo.Version" {
		t.Errorf("Version = %q", cfg.Version.Version)
	}
}

func TestVersionDirectiveRejects(t *testing.T) {
	for _, in := range []string{
		"version\n",
		"version Version\n",
		"version .Version\n",
		"version buildinfo.\n",
		"version a.B b.C\n",
		"version unknown internal/buildinfo.X\n",
		"version a.B\nversion c.D\n",
		"version commit a.B\nversion commit c.D\n",
	} {
		if _, err := Decode(parse(t, in)); err == nil {
			t.Errorf("%q should not have parsed", in)
		}
	}
}

func TestTagsDirective(t *testing.T) {
	cfg := decode(t, "tags netgo osusergo\ntags embed\n")
	if got := cfg.Tags; len(got) != 3 || got[0] != "netgo" || got[2] != "embed" {
		t.Errorf("Tags = %q", got)
	}
	if _, err := Decode(parse(t, "tags\n")); err == nil {
		t.Error("bare tags should not have parsed")
	}
}

// known and handlers are two tables over one closed set, split only because
// arity() reads the first and every handler calls it. Drift between them would
// mean a directive that parses and does nothing, or one that errors with an
// empty description.
func TestEveryDirectiveIsHandled(t *testing.T) {
	for name := range known {
		if handlers[name] == nil {
			t.Errorf("directive %q is documented but not handled", name)
		}
	}
	for name := range handlers {
		if known[name] == "" {
			t.Errorf("directive %q is handled but not documented", name)
		}
	}
}
