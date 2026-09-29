package lsp

import (
	"strings"

	"github.com/danielriddell21/letsgo/internal/config"
)

// formatEdits is letsgo fmt's own File.Format, wrapped as a whole-document
// LSP edit — ED-5 requires the two to be byte for byte identical, and
// reusing the same function is what makes that true by construction rather
// than by two implementations agreeing.
//
// A file that does not parse returns no edit: formatting invalid syntax
// would mean guessing what the author meant, which is diagnostics' job, not
// formatting's.
func formatEdits(path, text string) []TextEdit {
	f, err := config.Parse(path, []byte(text))
	if err != nil {
		return nil
	}

	formatted := string(f.Format())
	if formatted == text {
		return nil
	}

	lines := strings.Split(text, "\n")
	endLine := len(lines) - 1
	return []TextEdit{{
		Range:   Range{Start: Position{0, 0}, End: Position{endLine, len(lines[endLine])}},
		NewText: formatted,
	}}
}
