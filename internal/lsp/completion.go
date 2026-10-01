package lsp

import (
	"context"
	"sort"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/feature"
	"github.com/danielriddell21/letsgo/internal/plugin"
)

// completionContext is what precedes the cursor: the directive being written
// and the arguments already typed, and — when the cursor sits inside a block —
// that block's keyword.
type completionContext struct {
	tokens      []string // keyword, then each complete argument before the word being typed
	blockOpener string   // "" outside any block
}

// lineContext works out where the cursor sits from the parsed document. A line
// inside a block takes the block's keyword as its own, so `disable (` offers
// feature names on its lines the way `disable` does.
func lineContext(o outline, lineNo, character int) completionContext {
	lc := completionContext{blockOpener: o.blockAt(lineNo)}
	if lc.blockOpener != "" {
		lc.tokens = append(lc.tokens, lc.blockOpener)
	}

	d, ok := o.byLine[lineNo]
	if !ok {
		return lc
	}
	// A word the cursor is in or at the end of is being typed, not yet a
	// complete token of context.
	if !d.inBlock && d.kw.end < character {
		lc.tokens = append(lc.tokens, d.kw.text)
	}
	for _, w := range d.args {
		if w.end < character {
			lc.tokens = append(lc.tokens, w.text)
		}
	}
	return lc
}

// completions returns the completion list for one position in one document.
// exec controls whether target completion may run `go tool dist list` — off
// in --restricted mode, which runs nothing beyond the letsgo process itself.
func completions(ctx context.Context, path, text string, lineNo, character int, exec bool, targets targetLister) []CompletionItem {
	lc := lineContext(outlineOf(path, text), lineNo, character)

	if lc.blockOpener == "build" {
		return targetCompletions(ctx, exec, targets)
	}

	switch len(lc.tokens) {
	case 0:
		return directiveCompletions(path)
	case 1:
		return argumentCompletions(ctx, lc.tokens[0], nil, exec, targets)
	default:
		return argumentCompletions(ctx, lc.tokens[0], lc.tokens[1:], exec, targets)
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

func argumentCompletions(ctx context.Context, keyword string, priorArgs []string, exec bool, targets targetLister) []CompletionItem {
	switch keyword {
	case "build":
		return targetCompletions(ctx, exec, targets)
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

func targetCompletions(ctx context.Context, exec bool, targets targetLister) []CompletionItem {
	if !exec {
		return nil
	}
	list, err := targets(ctx)
	if err != nil {
		return nil
	}
	items := make([]CompletionItem, 0, len(list))
	for _, t := range list {
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
		items = append(items, CompletionItem{Label: string(f.Name), Kind: CompletionValue, Documentation: f.Summary})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Label < items[j].Label })
	return items
}
