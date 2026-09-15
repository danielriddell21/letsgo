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
