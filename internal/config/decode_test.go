package config

import (
	"strings"
	"testing"
)

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
		if handlers[name] == nil && !blockOnly[name] {
			t.Errorf("directive %q is documented but not handled", name)
		}
	}
	for name := range handlers {
		if known[name] == "" {
			t.Errorf("directive %q is handled but not documented", name)
		}
	}
	for name := range blockOnly {
		if known[name] == "" {
			t.Errorf("block %q is handled but not documented", name)
		}
		if handlers[name] != nil {
			t.Errorf("block %q also has a line handler, so one of them is dead", name)
		}
	}
}

func TestPluginDirective(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	cfg := decode(t, "plugin archive-layout letsgo-multi v0.1.0 "+digest+"\n")

	if len(cfg.Plugins) != 1 {
		t.Fatalf("Plugins = %+v", cfg.Plugins)
	}
	got := cfg.Plugins[0]
	if got.Hook != "archive-layout" || got.Command != "letsgo-multi" ||
		got.Version != "v0.1.0" || got.Digest != digest {
		t.Errorf("plugin = %+v", got)
	}
}

// A plugin that ran unpinned would be an unrecorded build input, which is the
// thing the whole contract exists to prevent.
func TestPluginDirectiveRequiresAPin(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, in := range []string{
		"plugin archive-layout letsgo-multi v0.1.0\n",
		"plugin archive-layout letsgo-multi v0.1.0 deadbeef\n",
		"plugin archive-layout letsgo-multi v0.1.0 sha256:short\n",
		"plugin archive-layout letsgo-multi v0.1.0 md5:" + strings.Repeat("a", 64) + "\n",
		"plugin archive-layout a v1 " + digest + "\nplugin archive-layout b v1 " + digest + "\n",
	} {
		if _, err := Decode(parse(t, in)); err == nil {
			t.Errorf("%q should not have parsed", in)
		}
	}
}

func TestVariantBlock(t *testing.T) {
	cfg := decode(t, "variant gui (\n\tbuild darwin/arm64 darwin/amd64\n\ttags ebiten\n)\n")

	if len(cfg.Variants) != 1 {
		t.Fatalf("Variants = %+v", cfg.Variants)
	}
	got := cfg.Variants[0]
	if got.Name != "gui" {
		t.Errorf("name = %q", got.Name)
	}
	if strings.Join(got.Targets, ",") != "darwin/arm64,darwin/amd64" {
		t.Errorf("targets = %q", got.Targets)
	}
	if strings.Join(got.Tags, ",") != "ebiten" {
		t.Errorf("tags = %q", got.Tags)
	}

	// Several variants are the point: a repository can have more than two
	// products from one source.
	cfg = decode(t, "variant gui (\n\tbuild darwin/arm64\n)\nvariant headless (\n\tbuild linux/amd64\n)\n")
	if len(cfg.Variants) != 2 {
		t.Errorf("Variants = %+v, want two", cfg.Variants)
	}
}

func TestVariantBlockRejects(t *testing.T) {
	for _, in := range []string{
		// A variant that inherited the matrix would build everywhere, which is
		// never why one exists.
		"variant gui (\n\ttags ebiten\n)\n",
		"variant (\n\tbuild linux/amd64\n)\n",
		"variant a b (\n\tbuild linux/amd64\n)\n",
		"variant gui (\n\tbuild linux/amd64\n\tbrew you/tap\n)\n",
		"variant gui (\n\tbuild linux/amd64\n\tproject other\n)\n",
		"variant gui (\n\tbuild linux/amd64\n)\nvariant gui (\n\tbuild linux/arm64\n)\n",
		"variant with_underscore (\n\tbuild linux/amd64\n)\n",
		// A block keyword used as a plain line parses and does nothing, so it
		// says so instead.
		"variant gui\n",
	} {
		if _, err := Decode(parse(t, in)); err == nil {
			t.Errorf("%q should not have parsed", in)
		}
	}
}

// A block that takes no name must not silently accept one.
func TestUnnamedBlocksRejectAName(t *testing.T) {
	if _, err := Decode(parse(t, "archive extra (\n\tREADME.md\n)\n")); err == nil {
		t.Error("a named archive block should not have parsed")
	}
}

func TestBrewDirective(t *testing.T) {
	cfg := decode(t, "brew you/tap\nbrew caveats \"needs a display\"\n")
	if cfg.BrewTap != "you/tap" {
		t.Errorf("BrewTap = %q", cfg.BrewTap)
	}
	if cfg.BrewCaveats != "needs a display" {
		t.Errorf("BrewCaveats = %q", cfg.BrewCaveats)
	}

	// The tap on its own is the common case and stays short.
	if cfg := decode(t, "brew you/tap\n"); cfg.BrewCaveats != "" {
		t.Errorf("BrewCaveats = %q, want empty", cfg.BrewCaveats)
	}
}

func TestBrewDirectiveRejects(t *testing.T) {
	for _, in := range []string{
		"brew\n",
		"brew not-a-tap\n",
		"brew you/tap\nbrew other/tap\n",
		"brew caveats one\nbrew caveats two\n",
		"brew caveats\n",
	} {
		if _, err := Decode(parse(t, in)); err == nil {
			t.Errorf("%q should not have parsed", in)
		}
	}
}
