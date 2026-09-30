package lsp_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/lsp"
)

// canRename is what a client sends to say a workspace edit may move files.
var canRename = map[string]any{"capabilities": map[string]any{"workspace": map[string]any{
	"workspaceEdit": map[string]any{"resourceOperations": []string{"rename"}},
}}}

func writeFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil { //nolint:gosec // a fixture
			t.Fatal(err)
		}
	}
}

func editJSON(t *testing.T, a lsp.CodeAction) string {
	t.Helper()
	if a.Edit == nil {
		t.Fatalf("action %q carries no edit", a.Title)
	}
	body, err := json.Marshal(a.Edit)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestLegacyConfigActionMovesTheFileUnderLetsgo(t *testing.T) {
	cmdPin := func(command string) string { return pinFor(command, oldDigest) + "\n" }
	tests := []struct {
		name  string
		files []string
		init  map[string]any
		text  string
		want  string // the action's title; "" means none
		from  string // the file it moves, and where to, relative to the repository
		to    string
	}{
		{"legacy only", []string{"letsgo-env.mod"}, canRename, cmdPin("letsgo-env"), "Move letsgo-env.mod to .letsgo/env.mod", "letsgo-env.mod", ".letsgo/env.mod"},
		{"third-party command", []string{"acme.mod"}, canRename, cmdPin("acme"), "Move acme.mod to .letsgo/acme.mod", "acme.mod", ".letsgo/acme.mod"},
		{"both exist", []string{"letsgo-env.mod", ".letsgo/env.mod"}, canRename, cmdPin("letsgo-env"), "", "", ""},
		{"already moved", []string{".letsgo/env.mod"}, canRename, cmdPin("letsgo-env"), "", "", ""},
		{"another plugin's file", []string{"letsgo-cask.mod"}, canRename, cmdPin("letsgo-env"), "", "", ""},
		{"client cannot rename", []string{"letsgo-env.mod"}, map[string]any{}, cmdPin("letsgo-env"), "", "", ""},
		{"command is a path", []string{"tools/env.mod"}, canRename, cmdPin("./tools/env"), "", "", ""},
		{"no pin", []string{"letsgo-env.mod"}, canRename, "build linux/amd64\n", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFiles(t, dir, tt.files...)
			c, uri := editorIn(t, lsp.Options{Restricted: true}, tt.init, dir, "letsgo.mod", tt.text)

			actions := codeActions(t, c, uri)
			if tt.want == "" {
				if len(actions) != 0 {
					t.Fatalf("actions = %+v, want none", actions)
				}
				return
			}
			if len(actions) != 1 || actions[0].Title != tt.want || actions[0].Kind != "quickfix" {
				t.Fatalf("actions = %+v, want one quickfix %q", actions, tt.want)
			}
			wantEdit, err := json.Marshal(map[string]any{"documentChanges": []any{map[string]any{
				"kind":   "rename",
				"oldUri": fileURI(dir) + "/" + tt.from,
				"newUri": fileURI(dir) + "/" + tt.to,
			}}})
			if err != nil {
				t.Fatal(err)
			}
			if got := editJSON(t, actions[0]); got != string(wantEdit) {
				t.Errorf("edit = %s, want %s", got, wantEdit)
			}
		})
	}
}

func TestGlobalDirectiveActionRemovesItFromLetsgoMod(t *testing.T) {
	t.Setenv(config.GlobalConfigEnvOverride, filepath.Join(t.TempDir(), "config.mod"))
	where, _, err := config.GlobalPath()
	if err != nil {
		t.Fatal(err)
	}
	title := func(directive string) string {
		return "Remove " + directive + " from letsgo.mod (it belongs in " + where + ")"
	}

	tests := []struct {
		name, file, text string
		restricted       bool
		titles           []string
		edits            [][2]int // per action: the first line removed, and the line the edit ends at
	}{
		{"first line", "letsgo.mod", "go /usr/bin/go\nbuild linux/amd64\n", false, []string{title("go")}, [][2]int{{0, 1}}},
		{"restricted too", "letsgo.mod", "go /usr/bin/go\nbuild linux/amd64\n", true, []string{title("go")}, [][2]int{{0, 1}}},
		{"two of them", "letsgo.mod", "go /usr/bin/go\nproxy https://example.com\n", false, []string{title("go"), title("proxy")}, [][2]int{{0, 1}, {1, 2}}},
		{"last line without a newline", "letsgo.mod", "build linux/amd64\ncolor never", false, []string{title("color")}, [][2]int{{1, 1}}},
		{"a repository directive", "letsgo.mod", "build linux/amd64\n", false, nil, nil},
		{"in the global config", "config.mod", "go /usr/bin/go\n", false, nil, nil},
		{"in a block", "letsgo.mod", "release (\n\tgo\n)\n", false, nil, nil},
		{"cannot be parsed", "letsgo.mod", "go (\n", false, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, uri := editorIn(t, lsp.Options{Restricted: tt.restricted}, canRename, t.TempDir(), tt.file, tt.text)

			actions := codeActions(t, c, uri)
			if len(actions) != len(tt.titles) {
				t.Fatalf("actions = %+v, want titles %q", actions, tt.titles)
			}
			for i, a := range actions {
				edits := a.Edit.Changes[uri]
				first, last := tt.edits[i][0], tt.edits[i][1]
				if a.Title != tt.titles[i] || len(edits) != 1 || edits[0].NewText != "" ||
					edits[0].Range.Start != (lsp.Position{Line: first}) || edits[0].Range.End.Line != last {
					t.Errorf("action %d = %+v edits %+v, want %q deleting from line %d to line %d", i, a, edits, tt.titles[i], first, last)
				}
			}
		})
	}
}
