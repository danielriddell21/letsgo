package lsp

import (
	"strings"

	"github.com/danielriddell21/letsgo/internal/config"
)

// documentSymbols builds the outline VS Code's Outline view and breadcrumbs
// show: one entry per top-level directive or block, a block's own lines
// nested underneath it.
func documentSymbols(path, text string) []DocumentSymbol {
	f, err := config.Parse(path, []byte(text))
	if err != nil {
		return nil
	}
	lines := strings.Split(text, "\n")

	var symbols []DocumentSymbol
	for _, stmt := range f.Stmts {
		switch s := stmt.(type) {
		case *config.Line:
			symbols = append(symbols, lineSymbol(lines, s.Keyword, s.Args, s.Pos()))
		case *config.Block:
			symbols = append(symbols, blockSymbol(lines, s))
		}
	}
	return symbols
}

func lineSymbol(lines []string, keyword string, args []string, pos config.Position) DocumentSymbol {
	parts := args
	if keyword != "" {
		parts = append([]string{keyword}, args...)
	}
	r := lineRange(lines, pos.Line)
	return DocumentSymbol{Name: strings.Join(parts, " "), Kind: SymbolKindField, Range: r, SelectionRange: r}
}

func blockSymbol(lines []string, b *config.Block) DocumentSymbol {
	name := b.Keyword
	if len(b.Args) > 0 {
		name += " " + strings.Join(b.Args, " ")
	}
	r := lineRange(lines, b.P.Line)

	children := make([]DocumentSymbol, 0, len(b.Lines))
	for _, line := range b.Lines {
		children = append(children, lineSymbol(lines, "", line.Args, line.Pos()))
		if line.Pos().Line-1 > r.End.Line {
			r.End = Position{Line: line.Pos().Line - 1, Character: len(safeLine(lines, line.Pos().Line-1))}
		}
	}
	return DocumentSymbol{Name: name, Kind: SymbolKindNamespace, Range: r, SelectionRange: r, Children: children}
}

func lineRange(lines []string, oneBasedLine int) Range {
	l := oneBasedLine - 1
	text := safeLine(lines, l)
	return Range{Start: Position{Line: l, Character: 0}, End: Position{Line: l, Character: len(text)}}
}

func safeLine(lines []string, i int) string {
	if i < 0 || i >= len(lines) {
		return ""
	}
	return lines[i]
}
