package lsp

import (
	"context"
	"testing"
)

func TestLineContext(t *testing.T) {
	tests := []struct {
		name      string
		lines     []string
		lineNo    int
		character int
		want      completionContext
	}{
		{"empty line", []string{""}, 0, 0, completionContext{tokens: nil}},
		{"typing the first word", []string{"bui"}, 0, 3, completionContext{tokens: nil}},
		{"first word complete, cursor after space", []string{"build "}, 0, 6, completionContext{tokens: []string{"build"}}},
		{"typing the second word", []string{"plugin arch"}, 0, 11, completionContext{tokens: []string{"plugin"}}},
		{"second word complete, cursor after space", []string{"plugin archive "}, 0, 16, completionContext{tokens: []string{"plugin", "archive"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lineContext(tt.lines, tt.lineNo, tt.character)
			if len(got.tokens) != len(tt.want.tokens) {
				t.Fatalf("tokens = %v, want %v", got.tokens, tt.want.tokens)
			}
			for i := range got.tokens {
				if got.tokens[i] != tt.want.tokens[i] {
					t.Errorf("tokens[%d] = %q, want %q", i, got.tokens[i], tt.want.tokens[i])
				}
			}
		})
	}
}

func TestOpenBlockKeyword(t *testing.T) {
	lines := []string{
		"build (",
		"  linux/amd64",
		"  windows/amd64",
		")",
		"tags foo",
	}
	if got := openBlockKeyword(lines, 2); got != "build" {
		t.Errorf("inside the block: openBlockKeyword = %q, want build", got)
	}
	if got := openBlockKeyword(lines, 4); got != "" {
		t.Errorf("after the block closes: openBlockKeyword = %q, want empty", got)
	}
	if got := openBlockKeyword(lines, 0); got != "" {
		t.Errorf("before the block opens: openBlockKeyword = %q, want empty", got)
	}
}

func TestCompletionsInsideABuildBlockOffersTargets(t *testing.T) {
	text := "build (\n  \n)\n"
	items := completions(context.Background(), "letsgo.mod", text, 1, 2, false, "")
	if items != nil {
		t.Errorf("items = %+v, want nil: exec is false, so no subprocess should run", items)
	}
}

func TestCompletionsWithNoTokensOffersDirectives(t *testing.T) {
	items := completions(context.Background(), "letsgo.mod", "", 0, 0, false, "")
	found := false
	for _, item := range items {
		if item.Label == "build" {
			found = true
		}
		if item.Kind != CompletionKeyword {
			t.Errorf("item %q Kind = %d, want CompletionKeyword", item.Label, item.Kind)
		}
	}
	if !found {
		t.Errorf("items = %+v, want a \"build\" directive", items)
	}
}

func TestCompletionsWithNoTokensOffersGlobalDirectivesForConfigMod(t *testing.T) {
	items := completions(context.Background(), "config.mod", "", 0, 0, false, "")
	found := false
	for _, item := range items {
		if item.Label == "go" {
			found = true
		}
		if item.Label == "build" {
			t.Errorf("items = %+v, want no repo-only directive like build", items)
		}
	}
	if !found {
		t.Errorf("items = %+v, want a \"go\" global directive", items)
	}
}

func TestArgumentCompletionsForPlugin(t *testing.T) {
	items := argumentCompletions(context.Background(), "plugin", nil, false, "")
	found := false
	for _, item := range items {
		if item.Label == "archive-layout" {
			found = true
		}
	}
	if !found {
		t.Errorf("items = %+v, want the archive-layout hook", items)
	}
}

func TestArgumentCompletionsForPluginNameFiltersByHook(t *testing.T) {
	items := argumentCompletions(context.Background(), "plugin", []string{"ldflags"}, false, "")
	if len(items) == 0 {
		t.Fatal("items is empty, want at least letsgo-env")
	}
	for _, item := range items {
		if item.Label != "letsgo-env" {
			t.Errorf("item = %q, want only letsgo-env for the ldflags hook", item.Label)
		}
	}
}

func TestArgumentCompletionsForPluginNameFallsBackToEveryKnownPlugin(t *testing.T) {
	items := argumentCompletions(context.Background(), "plugin", []string{"not-a-real-hook"}, false, "")
	if len(items) < 2 {
		t.Errorf("items = %+v, want every known plugin as a fallback", items)
	}
}

func TestArgumentCompletionsForDisableOffersDisableableFeatures(t *testing.T) {
	items := argumentCompletions(context.Background(), "disable", nil, false, "")
	if len(items) == 0 {
		t.Fatal("items is empty, want at least one disableable feature")
	}
	for i := 1; i < len(items); i++ {
		if items[i-1].Label > items[i].Label {
			t.Errorf("items not sorted: %q before %q", items[i-1].Label, items[i].Label)
		}
	}
}

func TestArgumentCompletionsForUnknownKeywordReturnsNil(t *testing.T) {
	if items := argumentCompletions(context.Background(), "not-a-real-directive", nil, false, ""); items != nil {
		t.Errorf("items = %+v, want nil", items)
	}
}

func TestTargetCompletionsIsEmptyWhenNotExec(t *testing.T) {
	if items := targetCompletions(context.Background(), false, ""); items != nil {
		t.Errorf("items = %+v, want nil in restricted mode", items)
	}
}
