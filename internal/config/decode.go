package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/danielriddell21/letsgo/modsyntax"
)

// mark records where a directive was written. The first occurrence wins: a
// repeated list directive is still one thing to point at, and the first line
// is where a reader starts looking.
func (c *Config) mark(key string, pos modsyntax.Position) {
	if c.Pos == nil {
		c.Pos = map[string]modsyntax.Position{}
	}
	if _, ok := c.Pos[key]; !ok {
		c.Pos[key] = pos
	}
}

// Decode interprets a parsed file.
func Decode(f *modsyntax.File) (*Config, error) {
	cfg := &Config{Budgets: map[string]string{}, BudgetPos: map[string]modsyntax.Position{}}
	seen := map[string]modsyntax.Position{}

	for _, stmt := range f.Stmts {
		switch s := stmt.(type) {
		case *modsyntax.Comment:
			continue
		case *modsyntax.Block:
			if err := decodeBlock(cfg, f.Name, seen, s); err != nil {
				return nil, err
			}
		case *modsyntax.Line:
			if err := decodeLine(cfg, f.Name, seen, s); err != nil {
				return nil, err
			}
		}
	}

	return cfg, nil
}

func decodeBlock(cfg *Config, file string, seen map[string]modsyntax.Position, b *modsyntax.Block) error {
	if err := checkKnown(file, b.Keyword, b.P); err != nil {
		return err
	}
	cfg.mark(b.Keyword, b.P)

	// A variant is the one block a file may have several of, because having
	// two products from one source is the whole point of it.
	if b.Keyword == "variant" {
		return applyVariant(cfg, file, b)
	}
	if len(b.Args) > 0 {
		return errAt(file, b.P, "%s takes no name before its block", b.Keyword)
	}
	if err := checkOnce(file, seen, b.Keyword, b.P); err != nil {
		return err
	}
	for _, line := range b.Lines {
		if err := apply(cfg, file, line); err != nil {
			return err
		}
	}
	return nil
}

func decodeLine(cfg *Config, file string, seen map[string]modsyntax.Position, line *modsyntax.Line) error {
	if err := checkKnown(file, line.Keyword, line.P); err != nil {
		return err
	}
	cfg.mark(line.Keyword, line.P)
	// Repeating a scalar directive is ambiguous: one of the two values would
	// silently win. Repeating a list directive is not.
	if isScalar(line.Keyword) {
		if err := checkOnce(file, seen, line.Keyword, line.P); err != nil {
			return err
		}
	}
	return apply(cfg, file, line)
}

func isScalar(keyword string) bool {
	switch keyword {
	case "project", "module":
		return true
	}
	return false
}

func checkKnown(file, keyword string, pos modsyntax.Position) error {
	if _, ok := known[keyword]; ok {
		return nil
	}
	if _, ok := globalKnown[keyword]; ok {
		return errAt(file, pos, "%s belongs in the global config, not letsgo.mod", keyword)
	}

	names := make([]string, 0, len(known))
	for name := range known {
		names = append(names, name)
	}
	sort.Strings(names)

	return unknownName(file, pos, "directive", keyword, names)
}

// nearestKeyword finds the name closest to keyword, for a did-you-mean
// suggestion. A prefix or a case difference is caught outright; anything else
// falls back to edit distance, so a transposed pair of letters (sbmo for
// sbom) still gets a suggestion rather than the full list.
func nearestKeyword(keyword string, names []string) string {
	for _, name := range names {
		if strings.EqualFold(name, keyword) || strings.HasPrefix(name, keyword) {
			return name
		}
	}

	best, bestDist := "", -1
	for _, name := range names {
		d := levenshtein(strings.ToLower(keyword), strings.ToLower(name))
		if bestDist == -1 || d < bestDist {
			best, bestDist = name, d
		}
	}

	// Worth suggesting only when the typo is close: past this, a guess is as
	// likely to be wrong as right, and the full list serves the reader better.
	if best != "" && bestDist <= (len(keyword)+1)/2 {
		return best
	}
	return ""
}

// levenshtein is the edit distance between two strings: the fewest
// insertions, deletions and substitutions that turn one into the other.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)

	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

func checkOnce(file string, seen map[string]modsyntax.Position, keyword string, pos modsyntax.Position) error {
	if first, ok := seen[keyword]; ok {
		return errAt(file, pos, "%s is already set at line %d", keyword, first.Line)
	}
	seen[keyword] = pos
	return nil
}

// unknownName reports a word that is not one of names, suggesting the nearest
// when there is one. The suggestion rides on the error as data, so an editor
// need not read it back out of the message.
func unknownName(file string, pos modsyntax.Position, what, word string, names []string) error {
	if near := nearestKeyword(word, names); near != "" {
		return &modsyntax.SyntaxError{
			File: file, Pos: pos, Wrong: word, Suggest: near,
			Msg: fmt.Sprintf("unknown %s %q; did you mean %q?", what, word, near),
		}
	}
	return errAt(file, pos, "unknown %s %q; valid %ss are %s", what, word, what, strings.Join(names, ", "))
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func arity(file string, line *modsyntax.Line) error {
	return errAt(file, line.P, "%s takes %s", line.Keyword, known[line.Keyword])
}

// errAt is a syntax error at pos, for a directive the decoder cannot accept.
func errAt(file string, pos modsyntax.Position, format string, args ...any) error {
	return &modsyntax.SyntaxError{File: file, Pos: pos, Msg: fmt.Sprintf(format, args...)}
}
