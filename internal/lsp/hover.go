package lsp

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/gobuild"
)

// hoverAt returns what to show for the word under the cursor: the directive's
// documentation over a line's first word, and — when live is set — what this
// machine says about a plugin pin or a build target there. live is off in a
// restricted workspace, where nothing may touch the plugin store, PATH or the
// go toolchain. Anywhere else it returns ok=false rather than guessing.
func hoverAt(ctx context.Context, path, text string, pos Position, live bool, goBin string) (contents string, ok bool) {
	lines := strings.Split(text, "\n")
	if pos.Line < 0 || pos.Line >= len(lines) {
		return "", false
	}
	tokens := tokensOf(lines[pos.Line])
	at := tokenAt(tokens, pos.Character)
	if at < 0 {
		return "", false
	}

	var parts []string
	if at == 0 {
		if doc, ok := directiveDoc(path, tokens[0].text); ok {
			parts = append(parts, doc)
		}
	}
	if live && kindOf(path) == kindRepo {
		if state := liveHover(ctx, path, lines, pos.Line, tokens, at, goBin); state != "" {
			parts = append(parts, state)
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, "\n\n"), true
}

func directiveDoc(path, keyword string) (string, bool) {
	var usage, doc string
	var ok bool
	if kindOf(path) == kindGlobal {
		usage, doc, ok = config.GlobalDoc(keyword)
	} else {
		usage, doc, ok = config.Doc(keyword)
	}
	if !ok {
		return "", false
	}
	if doc == "" {
		return usage, true
	}
	return usage + "\n\n" + doc, true
}

// liveHover is the machine-dependent half of a hover: a plugin pin's install
// state from anywhere on its line, or whether the target under the cursor is
// one the go toolchain can build. "" means there is nothing to add.
func liveHover(ctx context.Context, path string, lines []string, lineNo int, tokens []token, at int, goBin string) string {
	switch {
	case tokens[0].text == "plugin":
		pin, ok := pinOnLine(lines[lineNo])
		if !ok {
			return ""
		}
		return fmt.Sprintf("`%s %s`: %s", pin.command, pin.version, checkPin(filepath.Dir(path), pin).detail)
	case isTargetToken(tokens, at, openBlockKeyword(lines, lineNo)):
		return targetHover(ctx, tokens[at].text, goBin)
	}
	return ""
}

// isTargetToken reports whether the word at index at names a goos/goarch
// target: an argument of `build`, the first argument of `budget`, or any word
// inside a `build ( ... )` block.
func isTargetToken(tokens []token, at int, opener string) bool {
	text := tokens[at].text
	if text == "(" || text == ")" {
		return false
	}
	switch {
	case opener == "build":
		return true
	case tokens[0].text == "build":
		return at > 0
	case tokens[0].text == "budget":
		return at == 1
	}
	return false
}

// targetHover says whether the go toolchain can build for target. It asks the
// toolchain (`go tool dist list`, run once per server) rather than keeping a
// list; a target that does not parse is left to the diagnostics.
func targetHover(ctx context.Context, word, goBin string) string {
	target, err := gobuild.ParseTarget(word)
	if err != nil {
		return ""
	}
	supported, err := gobuild.Supported(ctx, goBin)
	if err != nil {
		return ""
	}
	if slices.Contains(supported, target) {
		return fmt.Sprintf("`%s`: a target the go toolchain can build.", target)
	}
	return fmt.Sprintf("`%s`: not a target the go toolchain can build.", target)
}
