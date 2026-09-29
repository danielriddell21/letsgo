package lsp

import (
	"strings"

	"github.com/danielriddell21/letsgo/internal/config"
)

// hoverAt returns the documentation for the directive under the cursor —
// only the first word of a line is ever a directive keyword, so anywhere
// else returns ok=false rather than guessing.
func hoverAt(path, text string, pos Position) (contents string, ok bool) {
	lines := strings.Split(text, "\n")
	if pos.Line < 0 || pos.Line >= len(lines) {
		return "", false
	}
	line := lines[pos.Line]

	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", false
	}
	first := fields[0]
	start := strings.Index(line, first)
	end := start + len(first)
	if pos.Character < start || pos.Character > end {
		return "", false
	}

	var usage, doc string
	if kindOf(path) == kindGlobal {
		usage, doc, ok = config.GlobalDoc(first)
	} else {
		usage, doc, ok = config.Doc(first)
	}
	if !ok {
		return "", false
	}
	if doc == "" {
		return usage, true
	}
	return usage + "\n\n" + doc, true
}
