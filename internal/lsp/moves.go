package lsp

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/plugin"
)

// pluginConfigDir is where a plugin's own config lives, relative to the
// repository root. It mirrors plan's pluginConfigDir.
const pluginConfigDir = ".letsgo"

// RenameFile is a workspace edit operation that moves a file or directory.
type RenameFile struct {
	Kind   string `json:"kind"` // always "rename"
	OldURI string `json:"oldUri"`
	NewURI string `json:"newUri"`
}

// moveActions offers the quick fixes that rearrange config files rather than
// edit a pin: moving a plugin's legacy root config under .letsgo/, and
// removing a global-only directive from letsgo.mod. Neither runs anything, so
// both are offered in a restricted workspace too.
func (s *Server) moveActions(uri, text string, r Range) []CodeAction {
	path := uriToPath(uri)
	if kindOf(path) != kindRepo {
		return nil
	}
	lines := strings.Split(text, "\n")
	var actions []CodeAction
	if s.canRename {
		actions = append(actions, legacyConfigActions(uri, filepath.Dir(path), outlineOf(path, text), r)...)
	}
	return append(actions, globalDirectiveActions(uri, path, text, lines, r)...)
}

// legacyConfigActions offers, on each pin line whose plugin still reads its
// config from <command>.mod at the repository root, to move that file to
// .letsgo/<short name>.mod — the move plan's legacy-config warning asks for.
// It stays silent when the new file already exists, since that is plan's
// Fail and there is no telling which of the two to keep.
func legacyConfigActions(uri, dir string, o outline, r Range) []CodeAction {
	var actions []CodeAction
	seen := map[string]bool{}
	for n := max(r.Start.Line, 0); n <= min(r.End.Line, o.last); n++ {
		pin, ok := o.pin(n)
		if !ok || seen[pin.command] || strings.ContainsAny(pin.command, `/\`) {
			continue
		}
		seen[pin.command] = true

		legacy := pin.command + ".mod"
		modern := filepath.Join(pluginConfigDir, plugin.ShortName(pin.command)+".mod")
		if !fileExists(filepath.Join(dir, legacy)) || fileExists(filepath.Join(dir, modern)) {
			continue
		}
		base := uri[:strings.LastIndex(uri, "/")+1]
		title := fmt.Sprintf("Move %s to %s", legacy, filepath.ToSlash(modern))
		actions = append(actions, CodeAction{
			Title: title,
			Kind:  codeActionQuickFix,
			Edit: &WorkspaceEdit{DocumentChanges: []any{RenameFile{
				Kind:   "rename",
				OldURI: base + url.PathEscape(legacy),
				NewURI: base + pluginConfigDir + "/" + url.PathEscape(plugin.ShortName(pin.command)+".mod"),
			}}},
		})
	}
	return actions
}

// globalDirectiveActions offers to delete a directive that belongs in the
// global config.mod from a letsgo.mod.
//
// It deliberately only deletes. Appending to the machine's config.mod from a
// repository's editor session would be an edit outside the workspace, to a
// file the server has not been told is open and may not exist, in a place
// some clients refuse to write; and the directives involved (token-command,
// proxy, tool paths) are ones a stray append could quietly change for every
// repository on the machine. The title says where the line belongs, so the
// person can paste it there themselves.
func globalDirectiveActions(uri, path, text string, lines []string, r Range) []CodeAction {
	f, err := config.Parse(path, []byte(text))
	if err != nil {
		return nil
	}
	var actions []CodeAction
	for _, stmt := range f.Stmts {
		line, ok := stmt.(*config.Line)
		if !ok {
			continue
		}
		n := line.P.Line - 1
		if _, _, global := config.GlobalDoc(line.Keyword); !global || n < r.Start.Line || n > r.End.Line || n >= len(lines) {
			continue
		}
		end := Position{Line: n, Character: len(lines[n])}
		if n+1 < len(lines) {
			end = Position{Line: n + 1}
		}
		actions = append(actions, CodeAction{
			Title: fmt.Sprintf("Remove %s from letsgo.mod (it belongs in %s)", line.Keyword, globalConfigName()),
			Kind:  codeActionQuickFix,
			Edit: &WorkspaceEdit{Changes: map[string][]TextEdit{uri: {{
				Range: Range{Start: Position{Line: n}, End: end},
			}}}},
		})
	}
	return actions
}

// globalConfigName says where the global config lives, for an action title.
func globalConfigName() string {
	if path, _, err := config.GlobalPath(); err == nil {
		return path
	}
	return "the global config.mod"
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
