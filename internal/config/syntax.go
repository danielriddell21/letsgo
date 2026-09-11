// Package config reads letsgo.mod, a configuration file written in the same
// line-and-block directive syntax as go.mod and go.work.
//
// The format is borrowed rather than invented. Every user of a Go-only release
// tool already reads this syntax fluently, it has no indentation significance
// and no type coercion to be surprised by, and a complete parser with useful
// error messages costs a couple of hundred lines and no dependencies.
//
// The package is split the way the go command splits its own: a syntax layer
// that understands lines, blocks and comments without knowing what any of them
// mean, and a semantic layer (decode.go) that interprets them. Keeping the two
// apart is what lets the formatter preserve a file it does not understand.
package config

import (
	"fmt"
	"strconv"
	"strings"
)

// Position is a one-based location in a file.
type Position struct {
	Line int
	Col  int
}

func (p Position) String() string { return fmt.Sprintf("%d:%d", p.Line, p.Col) }

// Stmt is one statement in a file.
type Stmt interface {
	Pos() Position
}

// Line is a single directive: a keyword followed by arguments.
type Line struct {
	Keyword string
	Args    []string
	Comment string // trailing comment, without the leading "//"
	P       Position
}

func (l *Line) Pos() Position { return l.P }

// Block is a parenthesised group of argument lines sharing one keyword.
type Block struct {
	Keyword string
	Lines   []*Line
	Comment string // trailing comment on the opening line
	P       Position
}

func (b *Block) Pos() Position { return b.P }

// Comment is a standalone comment line, or a blank line when Text is empty.
// Both are kept so that formatting preserves the shape the author chose.
type Comment struct {
	Text  string
	Blank bool
	P     Position
}

func (c *Comment) Pos() Position { return c.P }

// File is a parsed configuration file.
type File struct {
	Name  string
	Stmts []Stmt
}

// SyntaxError carries a position so the reader is told where to look.
type SyntaxError struct {
	File string
	Pos  Position
	Msg  string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("%s:%s: %s", e.File, e.Pos, e.Msg)
}

// Parse reads a configuration file.
func Parse(name string, data []byte) (*File, error) {
	file := &File{Name: name}

	var open *Block
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")

	for i, text := range lines {
		lineNo := i + 1

		// A trailing newline produces a final empty element that is not a line
		// of the file.
		if i == len(lines)-1 && text == "" {
			break
		}

		tokens, comment, err := tokenise(name, lineNo, text)
		if err != nil {
			return nil, err
		}

		if len(tokens) == 0 {
			stmt := &Comment{Text: comment, Blank: comment == "", P: Position{lineNo, 1}}
			if open != nil {
				// A comment inside a block is dropped rather than misplaced.
				// Blocks hold argument lines, and inventing a place to hang a
				// free-floating comment would mean the formatter could move it
				// somewhere the author did not put it.
				continue
			}
			file.Stmts = append(file.Stmts, stmt)
			continue
		}

		if open != nil {
			if tokens[0].text == ")" {
				if len(tokens) > 1 {
					return nil, errAt(name, tokens[1].pos, "unexpected %q after closing parenthesis", tokens[1].text)
				}
				open = nil
				continue
			}
			args := make([]string, 0, len(tokens))
			for _, tok := range tokens {
				if tok.text == "(" {
					return nil, errAt(name, tok.pos, "nested blocks are not supported")
				}
				args = append(args, tok.text)
			}
			open.Lines = append(open.Lines, &Line{
				Keyword: open.Keyword,
				Args:    args,
				Comment: comment,
				P:       Position{lineNo, tokens[0].pos.Col},
			})
			continue
		}

		if tokens[0].text == ")" {
			return nil, errAt(name, tokens[0].pos, "unexpected closing parenthesis")
		}
		if tokens[0].text == "(" {
			return nil, errAt(name, tokens[0].pos, "block must be introduced by a keyword")
		}

		keyword := tokens[0].text
		rest := tokens[1:]

		if len(rest) > 0 && rest[len(rest)-1].text == "(" {
			if len(rest) > 1 {
				return nil, errAt(name, rest[0].pos,
					"%s ( must open a block on its own line; move %q inside the block",
					keyword, rest[0].text)
			}
			open = &Block{Keyword: keyword, Comment: comment, P: Position{lineNo, tokens[0].pos.Col}}
			file.Stmts = append(file.Stmts, open)
			continue
		}

		args := make([]string, 0, len(rest))
		for _, tok := range rest {
			if tok.text == "(" || tok.text == ")" {
				return nil, errAt(name, tok.pos, "unexpected %q", tok.text)
			}
			args = append(args, tok.text)
		}
		file.Stmts = append(file.Stmts, &Line{
			Keyword: keyword,
			Args:    args,
			Comment: comment,
			P:       Position{lineNo, tokens[0].pos.Col},
		})
	}

	if open != nil {
		return nil, errAt(name, open.P, "%s ( is never closed", open.Keyword)
	}
	return file, nil
}

type token struct {
	text string
	pos  Position
}

// tokenise splits one line into tokens and its trailing comment.
func tokenise(file string, lineNo int, text string) ([]token, string, error) {
	var tokens []token
	runes := []rune(text)

	for i := 0; i < len(runes); {
		if runes[i] == ' ' || runes[i] == '\t' {
			i++
			continue
		}

		if runes[i] == '/' && i+1 < len(runes) && runes[i+1] == '/' {
			return tokens, strings.TrimSpace(string(runes[i+2:])), nil
		}

		start := i
		col := i + 1

		// Parentheses are self-delimiting, so "build(" opens a block just as
		// "build (" does. go.mod tokenises the same way, and a format that
		// borrows its syntax should not disagree with it about something this
		// visible. An argument that genuinely contains a parenthesis has to be
		// quoted, which is also true in go.mod.
		if runes[i] == '(' || runes[i] == ')' {
			tokens = append(tokens, token{text: string(runes[i]), pos: Position{lineNo, col}})
			i++
			continue
		}

		if runes[i] == '"' {
			i++
			for i < len(runes) && runes[i] != '"' {
				if runes[i] == '\\' && i+1 < len(runes) {
					i++
				}
				i++
			}
			if i >= len(runes) {
				return nil, "", errAt(file, Position{lineNo, col}, "unterminated quoted string")
			}
			i++ // closing quote
			unquoted, err := strconv.Unquote(string(runes[start:i]))
			if err != nil {
				return nil, "", errAt(file, Position{lineNo, col}, "invalid quoted string: %v", err)
			}
			tokens = append(tokens, token{text: unquoted, pos: Position{lineNo, col}})
			continue
		}

		for i < len(runes) && runes[i] != ' ' && runes[i] != '\t' {
			if runes[i] == '/' && i+1 < len(runes) && runes[i+1] == '/' {
				break
			}
			if runes[i] == '(' || runes[i] == ')' {
				break
			}
			i++
		}
		tokens = append(tokens, token{text: string(runes[start:i]), pos: Position{lineNo, col}})
	}

	return tokens, "", nil
}

func errAt(file string, pos Position, format string, args ...any) error {
	return &SyntaxError{File: file, Pos: pos, Msg: fmt.Sprintf(format, args...)}
}
