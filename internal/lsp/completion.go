package lsp

import (
	"context"
	"sort"
	"strings"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/feature"
	"github.com/danielriddell21/letsgo/internal/gobuild"
	"github.com/danielriddell21/letsgo/internal/plugin"
)

// completionContext is what precedes the cursor on the current line: the
// tokens already typed, and — when the cursor sits inside an unclosed block —
// that block's keyword.
type completionContext struct {
	tokens      []string // complete tokens before the word being typed
	blockOpener string   // "" outside any block
}

// lineContext works out where in the line the cursor sits: which tokens are
// already complete, and whether the cursor is inside an open block, by
// scanning every earlier line for an unbalanced "(".
//
// This is a token count, not a real parser state machine — good enough for
// completion, where returning an item the client's own filtering then
// discards costs nothing, but missing a genuine directive would.
func lineContext(lines []string, lineNo, character int) completionContext {
	line := ""
	if lineNo >= 0 && lineNo < len(lines) {
		line = lines[lineNo]
	}
	if character > len(line) {
		character = len(line)
	}
	prefix := line[:character]

	tokens := strings.Fields(prefix)
	if len(tokens) > 0 && character > 0 && !isSpace(prefix[character-1]) {
		// The cursor is inside or at the end of a word: that word is being
		// typed, not yet a complete token of context.
		tokens = tokens[:len(tokens)-1]
	}

	return completionContext{tokens: tokens, blockOpener: openBlockKeyword(lines, lineNo)}
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' }

// openBlockKeyword returns the keyword of the block still open when line
// lineNo starts, by counting parentheses on every earlier line.
func openBlockKeyword(lines []string, lineNo int) string {
	depth := 0
	opener := ""
	for i := 0; i < lineNo && i < len(lines); i++ {
		fields := strings.Fields(lines[i])
		for _, tok := range fields {
			depth += strings.Count(tok, "(") - strings.Count(tok, ")")
		}
		if depth > 0 && opener == "" && len(fields) > 0 {
			opener = fields[0]
		}
		if depth <= 0 {
			opener = ""
		}
	}
	return opener
}

// completions returns the completion list for one position in one document.
// exec controls whether target completion may run `go tool dist list` — off
// in --restricted mode, which runs nothing beyond the letsgo process itself.
func completions(ctx context.Context, path, text string, lineNo, character int, exec bool, goBin string) []CompletionItem {
	lines := strings.Split(text, "\n")
	lc := lineContext(lines, lineNo, character)

	if lc.blockOpener == "build" {
		return targetCompletions(ctx, exec, goBin)
	}

	switch len(lc.tokens) {
	case 0:
		return directiveCompletions(path)
	case 1:
		return argumentCompletions(ctx, lc.tokens[0], nil, exec, goBin)
	default:
		return argumentCompletions(ctx, lc.tokens[0], lc.tokens[1:], exec, goBin)
	}
}

func directiveCompletions(path string) []CompletionItem {
	var names []string
	var doc func(string) (string, string, bool)
	if kindOf(path) == kindGlobal {
		names, doc = config.GlobalDirectives(), config.GlobalDoc
	} else {
		names, doc = config.Directives(), config.Doc
	}

	items := make([]CompletionItem, 0, len(names))
	for _, name := range names {
		usage, text, _ := doc(name)
		items = append(items, CompletionItem{Label: name, Kind: CompletionKeyword, Detail: usage, Documentation: text})
	}
	return items
}

func argumentCompletions(ctx context.Context, keyword string, priorArgs []string, exec bool, goBin string) []CompletionItem {
	switch keyword {
	case "build":
		return targetCompletions(ctx, exec, goBin)
	case "plugin":
		if len(priorArgs) == 0 {
			return hookCompletions()
		}
		return pluginNameCompletions(priorArgs[0])
	case "disable":
		return featureCompletions(func(f feature.Feature) bool { return f.Disable })
	case "require":
		return featureCompletions(func(f feature.Feature) bool { return f.Require })
	default:
		return nil
	}
}

func targetCompletions(ctx context.Context, exec bool, goBin string) []CompletionItem {
	if !exec {
		return nil
	}
	targets, err := gobuild.Supported(ctx, goBin)
	if err != nil {
		return nil
	}
	items := make([]CompletionItem, 0, len(targets))
	for _, t := range targets {
		items = append(items, CompletionItem{Label: t.String(), Kind: CompletionValue})
	}
	return items
}

func hookCompletions() []CompletionItem {
	items := make([]CompletionItem, 0, len(plugin.Hooks))
	for _, h := range plugin.Hooks {
		items = append(items, CompletionItem{Label: string(h), Kind: CompletionValue})
	}
	return items
}

// pluginNameCompletions offers letsgo's own first-party plugins, preferring
// the ones that answer the hook already typed — a pin naming the wrong hook
// for a first-party plugin is a mistake `letsgo plan` would catch anyway, but
// completion can steer around it for free.
func pluginNameCompletions(hook string) []CompletionItem {
	items := make([]CompletionItem, 0, len(plugin.Known))
	for _, k := range plugin.Known {
		if string(k.Hook) != hook {
			continue
		}
		items = append(items, CompletionItem{Label: k.Command, Kind: CompletionModule, Detail: string(k.Hook), Documentation: k.Summary})
	}
	if len(items) > 0 {
		return items
	}
	// An unrecognised or not-yet-typed hook: fall back to every first-party
	// plugin rather than offering nothing.
	for _, k := range plugin.Known {
		items = append(items, CompletionItem{Label: k.Command, Kind: CompletionModule, Detail: string(k.Hook), Documentation: k.Summary})
	}
	return items
}

func featureCompletions(want func(feature.Feature) bool) []CompletionItem {
	items := make([]CompletionItem, 0, len(feature.All))
	for _, f := range feature.All {
		if !want(f) {
			continue
		}
		items = append(items, CompletionItem{Label: f.Name, Kind: CompletionValue, Documentation: f.Summary})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Label < items[j].Label })
	return items
}
