package lsp

import (
	"fmt"
	"regexp"
	"strings"
)

// didYouMeanMessage matches the suggestion the decoder attaches to an unknown
// directive or feature name.
var didYouMeanMessage = regexp.MustCompile(`unknown (?:directive|feature) "([^"]+)"; did you mean "([^"]+)"\?`)

// didYouMeanActions offers to replace a misspelled directive or feature name
// with the one the decoder suggested. It works from the diagnostic's own
// message, so the editor never carries a second copy of the directive table,
// and it needs no lookup, so it is offered in a restricted workspace too.
func didYouMeanActions(uri, text string, diagnostics []Diagnostic) []CodeAction {
	lines := strings.Split(text, "\n")
	var actions []CodeAction
	for _, d := range diagnostics {
		m := didYouMeanMessage.FindStringSubmatch(d.Message)
		if m == nil || d.Range.Start.Line < 0 || d.Range.Start.Line >= len(lines) {
			continue
		}
		wrong, right := m[1], m[2]
		line := lines[d.Range.Start.Line]
		from := min(d.Range.Start.Character, len(line))
		i := strings.Index(line[from:], wrong)
		if i < 0 {
			continue
		}
		start := from + i
		actions = append(actions, CodeAction{
			Title: fmt.Sprintf("Change %s to %s", wrong, right),
			Kind:  codeActionQuickFix,
			Edit: &WorkspaceEdit{Changes: map[string][]TextEdit{uri: {{
				Range: Range{
					Start: Position{Line: d.Range.Start.Line, Character: start},
					End:   Position{Line: d.Range.Start.Line, Character: start + len(wrong)},
				},
				NewText: right,
			}}}},
		})
	}
	return actions
}
