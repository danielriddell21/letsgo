package lsp

import "testing"

func TestDocumentSymbolsReturnsNilOnAParseError(t *testing.T) {
	if symbols := documentSymbols("letsgo.mod", "build (\n"); symbols != nil {
		t.Errorf("symbols = %+v, want nil", symbols)
	}
}

func TestDocumentSymbolsCoversTopLevelLines(t *testing.T) {
	text := "project foo\ntags bar\n"
	symbols := documentSymbols("letsgo.mod", text)
	if len(symbols) != 2 {
		t.Fatalf("len(symbols) = %d, want 2: %+v", len(symbols), symbols)
	}
	if symbols[0].Name != "project foo" {
		t.Errorf("symbols[0].Name = %q, want %q", symbols[0].Name, "project foo")
	}
	if symbols[0].Kind != SymbolKindField {
		t.Errorf("symbols[0].Kind = %d, want SymbolKindField", symbols[0].Kind)
	}
	if symbols[1].Name != "tags bar" {
		t.Errorf("symbols[1].Name = %q, want %q", symbols[1].Name, "tags bar")
	}
}

func TestDocumentSymbolsNestsBlockChildrenWithNoLeadingSpace(t *testing.T) {
	text := "build (\n  linux/amd64\n  windows/amd64\n)\n"
	symbols := documentSymbols("letsgo.mod", text)
	if len(symbols) != 1 {
		t.Fatalf("len(symbols) = %d, want 1: %+v", len(symbols), symbols)
	}
	block := symbols[0]
	if block.Name != "build" {
		t.Errorf("block.Name = %q, want %q", block.Name, "build")
	}
	if block.Kind != SymbolKindNamespace {
		t.Errorf("block.Kind = %d, want SymbolKindNamespace", block.Kind)
	}
	if len(block.Children) != 2 {
		t.Fatalf("len(Children) = %d, want 2: %+v", len(block.Children), block.Children)
	}
	if block.Children[0].Name != "linux/amd64" {
		t.Errorf("Children[0].Name = %q, want %q (no leading space)", block.Children[0].Name, "linux/amd64")
	}
	if block.Children[1].Name != "windows/amd64" {
		t.Errorf("Children[1].Name = %q, want %q", block.Children[1].Name, "windows/amd64")
	}
}

func TestDocumentSymbolsBlockRangeCoversItsLastChild(t *testing.T) {
	text := "build (\n  linux/amd64\n  windows/amd64\n)\ntags after\n"
	symbols := documentSymbols("letsgo.mod", text)
	if len(symbols) != 2 {
		t.Fatalf("len(symbols) = %d, want 2: %+v", len(symbols), symbols)
	}
	block := symbols[0]
	if block.Range.End.Line != 2 {
		t.Errorf("block.Range.End.Line = %d, want 2 (the last child line)", block.Range.End.Line)
	}
}

func TestSafeLineIsEmptyOutOfRange(t *testing.T) {
	lines := []string{"a", "b"}
	if got := safeLine(lines, -1); got != "" {
		t.Errorf("safeLine(-1) = %q, want empty", got)
	}
	if got := safeLine(lines, 2); got != "" {
		t.Errorf("safeLine(2) = %q, want empty", got)
	}
	if got := safeLine(lines, 1); got != "b" {
		t.Errorf("safeLine(1) = %q, want %q", got, "b")
	}
}
