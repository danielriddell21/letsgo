package lsp

import "testing"

func TestHoverAtReturnsDocsForARepoDirective(t *testing.T) {
	contents, ok := hoverAt("letsgo.mod", "build linux/amd64\n", Position{Line: 0, Character: 2})
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if contents == "" {
		t.Error("contents is empty")
	}
}

func TestHoverAtReturnsDocsForAGlobalDirective(t *testing.T) {
	contents, ok := hoverAt("config.mod", "go /usr/bin/go\n", Position{Line: 0, Character: 0})
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if contents == "" {
		t.Error("contents is empty")
	}
}

func TestHoverAtIsFalseOutsideTheFirstWord(t *testing.T) {
	if _, ok := hoverAt("letsgo.mod", "build linux/amd64\n", Position{Line: 0, Character: 10}); ok {
		t.Error("ok = true, want false: the cursor is over the argument, not the directive")
	}
}

func TestHoverAtIsFalseForAnUnknownDirective(t *testing.T) {
	if _, ok := hoverAt("letsgo.mod", "not-a-real-directive foo\n", Position{Line: 0, Character: 0}); ok {
		t.Error("ok = true, want false")
	}
}

func TestHoverAtIsFalseForABlankLine(t *testing.T) {
	if _, ok := hoverAt("letsgo.mod", "\n", Position{Line: 0, Character: 0}); ok {
		t.Error("ok = true, want false")
	}
}

func TestHoverAtIsFalseForAnOutOfRangeLine(t *testing.T) {
	if _, ok := hoverAt("letsgo.mod", "build linux/amd64\n", Position{Line: 5, Character: 0}); ok {
		t.Error("ok = true, want false")
	}
}
