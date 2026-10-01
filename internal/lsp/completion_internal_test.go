package lsp

import (
	"context"
	"slices"
	"testing"

	"github.com/danielriddell21/letsgo/internal/gobuild"
)

func TestLineContext(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		lineNo    int
		character int
		want      completionContext
	}{
		{"empty line", "", 0, 0, completionContext{tokens: nil}},
		{"typing the first word", "bui", 0, 3, completionContext{tokens: nil}},
		{"first word complete, cursor after space", "build ", 0, 6, completionContext{tokens: []string{"build"}}},
		{"typing the second word", "plugin arch", 0, 11, completionContext{tokens: []string{"plugin"}}},
		{"second word complete, cursor after space", "plugin archive ", 0, 16, completionContext{tokens: []string{"plugin", "archive"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lineContext(outlineOf("letsgo.mod", tt.text), tt.lineNo, tt.character)
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

func TestLineContextInsideABlock(t *testing.T) {
	tests := []struct {
		name, text string
		lineNo     int
		character  int
		want       completionContext
	}{
		{"empty line in a block", "disable (\n  \n)\n", 1, 2, completionContext{tokens: []string{"disable"}, blockOpener: "disable"}},
		{"typing in a block", "require (\n  prov\n)\n", 1, 6, completionContext{tokens: []string{"require"}, blockOpener: "require"}},
		{"second word in a block", "plugin (\n  archive \n)\n", 1, 10, completionContext{tokens: []string{"plugin", "archive"}, blockOpener: "plugin"}},
		{"unclosed block", "disable (\n  ", 1, 2, completionContext{tokens: []string{"disable"}, blockOpener: "disable"}},
		{"the closing line", "disable (\n  sbom\n)\n", 2, 0, completionContext{}},
		{"after the block", "disable (\n  sbom\n)\n\n", 3, 0, completionContext{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lineContext(outlineOf("letsgo.mod", tt.text), tt.lineNo, tt.character)
			if got.blockOpener != tt.want.blockOpener || !slices.Equal(got.tokens, tt.want.tokens) {
				t.Errorf("lineContext = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCompletionsInsideABlockOfFeatures(t *testing.T) {
	for _, keyword := range []string{"disable", "require"} {
		items := completions(context.Background(), "letsgo.mod", keyword+" (\n  \n)\n", 1, 2, false, goTargets)
		if len(items) == 0 {
			t.Fatalf("%s block: no completions", keyword)
		}
		for _, it := range items {
			if it.Kind == CompletionKeyword {
				t.Errorf("%s block offers the directive %q, want feature names", keyword, it.Label)
			}
		}
	}
}

func TestCompletionsInsideABuildBlockOffersTargets(t *testing.T) {
	text := "build (\n  \n)\n"
	items := completions(context.Background(), "letsgo.mod", text, 1, 2, false, goTargets)
	if items != nil {
		t.Errorf("items = %+v, want nil: exec is false, so no subprocess should run", items)
	}
}

func TestCompletionsWithNoTokensOffersDirectives(t *testing.T) {
	items := completions(context.Background(), "letsgo.mod", "", 0, 0, false, goTargets)
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
	items := completions(context.Background(), "config.mod", "", 0, 0, false, goTargets)
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
	items := argumentCompletions(context.Background(), "plugin", nil, false, goTargets)
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
	items := argumentCompletions(context.Background(), "plugin", []string{"ldflags"}, false, goTargets)
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
	items := argumentCompletions(context.Background(), "plugin", []string{"not-a-real-hook"}, false, goTargets)
	if len(items) < 2 {
		t.Errorf("items = %+v, want every known plugin as a fallback", items)
	}
}

func TestArgumentCompletionsForDisableOffersDisableableFeatures(t *testing.T) {
	items := argumentCompletions(context.Background(), "disable", nil, false, goTargets)
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
	if items := argumentCompletions(context.Background(), "not-a-real-directive", nil, false, goTargets); items != nil {
		t.Errorf("items = %+v, want nil", items)
	}
}

func TestArgumentCompletionsForBuildOffersTargets(t *testing.T) {
	items := argumentCompletions(context.Background(), "build", nil, true, goTargets)
	if len(items) == 0 {
		t.Fatal("items is empty, want the toolchain's supported targets")
	}
}

func TestArgumentCompletionsForRequireOffersRequireableFeatures(t *testing.T) {
	items := argumentCompletions(context.Background(), "require", nil, false, goTargets)
	if len(items) == 0 {
		t.Fatal("items is empty, want at least one requireable feature")
	}
}

func TestTargetCompletionsIsEmptyWhenNotExec(t *testing.T) {
	if items := targetCompletions(context.Background(), false, goTargets); items != nil {
		t.Errorf("items = %+v, want nil in restricted mode", items)
	}
}

func TestTargetCompletionsListsTheHostToolchainsTargets(t *testing.T) {
	items := targetCompletions(context.Background(), true, goTargets)
	if len(items) == 0 {
		t.Fatal("items is empty, want the toolchain's supported targets")
	}
}

// goTargets lists targets from the go on PATH, as a server with no GoBin does.
func goTargets(ctx context.Context) ([]gobuild.Target, error) {
	return gobuild.Supported(ctx, "")
}
