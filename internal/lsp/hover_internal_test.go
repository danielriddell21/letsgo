package lsp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/pluginstore"
)

func TestHoverAtDocs(t *testing.T) {
	tests := []struct {
		name, path, text string
		pos              Position
		live             bool
		want             string // substring; "" means no hover
	}{
		{"repo directive", "letsgo.mod", "build linux/amd64\n", Position{0, 2}, false, "build <goos/goarch>"},
		{"global directive", "config.mod", "go /usr/bin/go\n", Position{0, 0}, false, "go <path>"},
		{"end of the word", "letsgo.mod", "build linux/amd64\n", Position{0, 5}, false, "build <goos/goarch>"},
		{"argument, not live", "letsgo.mod", "build linux/amd64\n", Position{0, 10}, false, ""},
		{"unknown directive", "letsgo.mod", "not-a-real-directive foo\n", Position{0, 0}, true, ""},
		{"blank line", "letsgo.mod", "\n", Position{0, 0}, true, ""},
		{"line out of range", "letsgo.mod", "build linux/amd64\n", Position{5, 0}, true, ""},
		{"negative line", "letsgo.mod", "build linux/amd64\n", Position{-1, 0}, true, ""},
		{"inside a comment", "letsgo.mod", "// build linux/amd64\n", Position{0, 10}, true, ""},
		{"syntax-only file", ".letsgo/env.mod", "build linux/amd64\n", Position{0, 10}, true, ""},
		{"block keyword", "letsgo.mod", "disable (\n  sbom\n)\n", Position{0, 3}, false, "disable"},
		{"block entry has no directive doc", "letsgo.mod", "disable (\n  sbom\n)\n", Position{1, 3}, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := hoverAt(context.Background(), tt.path, tt.text, tt.pos, tt.live, goTargets, "")
			if ok != (tt.want != "") || !strings.Contains(got, tt.want) {
				t.Errorf("hoverAt = %q, %v; want a hover containing %q", got, ok, tt.want)
			}
		})
	}
}

func TestHoverAtTargets(t *testing.T) {
	tests := []struct {
		name, text string
		pos        Position
		live       bool
		want       string // substring; "" means no hover
	}{
		{"supported", "build linux/amd64\n", Position{0, 10}, true, "`linux/amd64`: a target the go toolchain can build"},
		{"unsupported", "build plan9/zzz\n", Position{0, 8}, true, "`plan9/zzz`: not a target the go toolchain can build"},
		{"second of several", "build linux/amd64 darwin/arm64\n", Position{0, 20}, true, "`darwin/arm64`: a target"},
		{"block entry", "build (\n  linux/arm64\n)\n", Position{1, 4}, true, "`linux/arm64`: a target"},
		{"block opener", "build (\n  linux/arm64\n)\n", Position{0, 6}, true, ""},
		{"budget target", "budget linux/amd64 10MB\n", Position{0, 10}, true, "`linux/amd64`: a target"},
		{"budget size", "budget linux/amd64 10MB\n", Position{0, 20}, true, ""},
		{"not a target", "build notatarget\n", Position{0, 8}, true, ""},
		{"restricted", "build linux/amd64\n", Position{0, 10}, false, ""},
		{"other directive", "tags linux/amd64\n", Position{0, 8}, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := hoverAt(context.Background(), "letsgo.mod", tt.text, tt.pos, tt.live, goTargets, "")
			if ok != (tt.want != "") || !strings.Contains(got, tt.want) {
				t.Errorf("hoverAt = %q, %v; want a hover containing %q", got, ok, tt.want)
			}
		})
	}
}

func TestCheckPin(t *testing.T) {
	const content = "the pinned build"
	tests := []struct {
		name    string
		inStore string // content installed in the store, "" for none
		onPath  string // content installed on PATH, "" for none
		tamper  bool   // rewrite the store entry after installing it
		command string
		state   pinState
		detail  string
	}{
		{"in the store", content, "", false, "letsgo-env", pinInstalled, "installed in the plugin store: "},
		{"on PATH", "", content, false, "letsgo-env", pinInstalled, "on PATH with the pinned digest: "},
		{"store wins over PATH", content, "other", false, "letsgo-env", pinInstalled, "installed in the plugin store: "},
		{"missing", "", "", false, "letsgo-env", pinMissing, "not installed; `letsgo plugin install`"},
		{"different binary on PATH", "", "another build", false, "letsgo-env", pinMismatch, "digest mismatch: the pin is sha256:"},
		{"tampered store entry", content, "", true, "letsgo-env", pinBroken, "no longer hashes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storeDir, pathDir := IsolatePlugins(t)
			digest := PinnedTool(t, t.TempDir(), "scratch", content)

			if tt.inStore != "" {
				store, err := pluginstore.Open(storeDir, "")
				if err != nil {
					t.Fatal(err)
				}
				path, err := store.Put(digest, tt.command, []byte(tt.inStore))
				if err != nil {
					t.Fatal(err)
				}
				if tt.tamper {
					if err := os.WriteFile(path, []byte("tampered"), 0o755); err != nil { //nolint:gosec // a plugin must be executable
						t.Fatal(err)
					}
				}
			}
			if tt.onPath != "" {
				PinnedTool(t, pathDir, tt.command, tt.onPath)
			}

			got := checkPin(t.TempDir(), "", pinLine{command: tt.command, version: "v0.1.0", digest: digest})
			if got.state != tt.state || !strings.Contains(got.detail, tt.detail) {
				t.Errorf("checkPin = %+v; want state %d, detail containing %q", got, tt.state, tt.detail)
			}
		})
	}
}

func TestCheckPinResolvesARelativeCommandAgainstTheRepository(t *testing.T) {
	IsolatePlugins(t)
	repo := t.TempDir()
	digest := PinnedTool(t, repo, "tools/env", "in the repository")

	got := checkPin(repo, "", pinLine{command: "./tools/env", digest: digest})
	if got.state != pinInstalled {
		t.Errorf("checkPin = %+v, want installed", got)
	}
}

func TestCheckPinLeavesNoStoreBehind(t *testing.T) {
	storeDir, _ := IsolatePlugins(t)
	absent := filepath.Join(storeDir, "never-created")
	t.Setenv(pluginstore.StoreEnvOverride, absent)

	checkPin(t.TempDir(), "", pinLine{command: "letsgo-env", digest: "sha256:abc"})
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Errorf("stat %s: %v; want the store left uncreated", absent, err)
	}
}

func TestHoverAtShowsAPinsInstallState(t *testing.T) {
	_, pathDir := IsolatePlugins(t)
	digest := PinnedTool(t, pathDir, "letsgo-env", "the env plugin")
	line := "plugin ldflags letsgo-env v0.1.0 " + digest + "\n"

	tests := []struct {
		name string
		pos  Position
		live bool
		want string
	}{
		{"over the directive, docs first", Position{0, 2}, true, "Pins an external program"},
		{"over the directive, then state", Position{0, 2}, true, "`letsgo-env v0.1.0`: on PATH with the pinned digest"},
		{"over the command", Position{0, 18}, true, "`letsgo-env v0.1.0`: on PATH"},
		{"over the digest", Position{0, 40}, true, "`letsgo-env v0.1.0`: on PATH"},
		{"restricted shows docs only", Position{0, 2}, false, "Pins an external program"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := hoverAt(context.Background(), "letsgo.mod", line, tt.pos, tt.live, goTargets, "")
			if !ok || !strings.Contains(got, tt.want) {
				t.Errorf("hoverAt = %q, %v; want a hover containing %q", got, ok, tt.want)
			}
		})
	}

	if got, _ := hoverAt(context.Background(), "letsgo.mod", line, Position{0, 18}, false, goTargets, ""); got != "" {
		t.Errorf("restricted hover over the command = %q, want none", got)
	}
	if got, ok := hoverAt(context.Background(), "letsgo.mod", "plugin ldflags letsgo-env\n", Position{0, 18}, true, goTargets, ""); ok {
		t.Errorf("hover over an unpinned plugin line = %q, want none", got)
	}
}
