package lsp

import "strings"

// token is one whitespace-separated word of a line, with the byte span it
// occupies so an edit or a hover can be placed on it.
type token struct {
	text       string
	start, end int
}

// tokensOf splits a line into words, dropping a trailing // comment. It works
// on the text rather than the parsed file because the parser records where a
// line begins, not where each argument does, and an editor needs the latter.
func tokensOf(line string) []token {
	if i := strings.Index(line, "//"); i >= 0 {
		line = line[:i]
	}

	var tokens []token
	for i := 0; i < len(line); {
		if isSpace(line[i]) {
			i++
			continue
		}
		start := i
		for i < len(line) && !isSpace(line[i]) {
			i++
		}
		tokens = append(tokens, token{line[start:i], start, i})
	}
	return tokens
}

// tokenAt is the index of the token the cursor touches, or -1. A cursor just
// past the last character of a word still counts as on it, the way editors
// place it after a click at the end of a word.
func tokenAt(tokens []token, character int) int {
	for i, t := range tokens {
		if character >= t.start && character <= t.end {
			return i
		}
	}
	return -1
}
