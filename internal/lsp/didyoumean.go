package lsp

import (
	"fmt"
	"strings"
)

// didYouMeanActions offers to replace a misspelled directive or feature name
// with the one the config reader suggested. It works from the suggestion the
// diagnostic carries, so the editor never holds a second copy of the
// directive table, and it needs no lookup, so it is offered in a restricted
// workspace too.
func didYouMeanActions(uri, text string, diagnostics []Diagnostic) []CodeAction {
	lines := strings.Split(text, "\n")
	var actions []CodeAction
	for _, d := range diagnostics {
		s := d.Data
		at := d.Range.Start
		if s == nil || at.Line < 0 || at.Line >= len(lines) {
			continue
		}
		line := lines[at.Line]
		end := at.Character + len(s.Wrong)
		if at.Character < 0 || end > len(line) || line[at.Character:end] != s.Wrong {
			continue
		}
		actions = append(actions, CodeAction{
			Title: fmt.Sprintf("Change %s to %s", s.Wrong, s.Suggest),
			Kind:  codeActionQuickFix,
			Edit: &WorkspaceEdit{Changes: map[string][]TextEdit{uri: {{
				Range:   Range{Start: at, End: Position{Line: at.Line, Character: end}},
				NewText: s.Suggest,
			}}}},
		})
	}
	return actions
}
