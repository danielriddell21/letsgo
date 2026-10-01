package lsp

import "github.com/danielriddell21/letsgo/internal/config"

// word is one argument of a directive and the character range it occupies.
type word struct {
	text       string
	start, end int
}

// directive is one directive of the document, read by the config parser: a
// line, or one line of a block, in which case keyword is the block's.
type directive struct {
	keyword string
	inBlock bool
	kw      word // the keyword as written; zero for a line inside a block
	args    []word
}

// block is a parenthesised group as the parser read it. closeLine is -1 when
// the document ends inside it.
type block struct {
	keyword             string
	openLine, closeLine int
}

// outline is a document read through config.ParseLenient, the parser `letsgo
// plan` uses, so the editor and the tool cannot disagree about where a word or
// a block is. Lines the parser cannot read are absent.
type outline struct {
	byLine map[int]directive
	blocks []block
	last   int // the last line holding a directive
}

func outlineOf(path, text string) outline {
	o := outline{byLine: map[int]directive{}}
	f, _ := config.ParseLenient(path, []byte(text))
	if f == nil {
		return o
	}
	for _, stmt := range f.Stmts {
		switch s := stmt.(type) {
		case *config.Line:
			o.byLine[s.P.Line-1] = directive{keyword: s.Keyword, kw: wordOf(s.Keyword, s.KeywordSpan), args: wordsOf(s.Args, s.ArgSpans)}
		case *config.Block:
			o.byLine[s.P.Line-1] = directive{keyword: s.Keyword, kw: wordOf(s.Keyword, s.KeywordSpan), args: wordsOf(s.Args, s.ArgSpans)}
			closeLine := -1
			if s.Close.Line > 0 {
				closeLine = s.Close.Line - 1
			}
			o.blocks = append(o.blocks, block{keyword: s.Keyword, openLine: s.P.Line - 1, closeLine: closeLine})
			for _, l := range s.Lines {
				o.byLine[l.P.Line-1] = directive{keyword: s.Keyword, inBlock: true, args: wordsOf(l.Args, l.ArgSpans)}
			}
		}
	}
	for n := range o.byLine {
		o.last = max(o.last, n)
	}
	return o
}

func wordOf(text string, s config.Span) word {
	return word{text: text, start: s.Col - 1, end: s.End - 1}
}

func wordsOf(texts []string, spans []config.Span) []word {
	words := make([]word, len(texts))
	for i, text := range texts {
		words[i] = wordOf(text, spans[i])
	}
	return words
}

// blockAt is the keyword of the block line falls inside, "" outside any. The
// lines carrying the parentheses are not inside.
func (o outline) blockAt(line int) string {
	for _, b := range o.blocks {
		if line > b.openLine && (b.closeLine < 0 || line < b.closeLine) {
			return b.keyword
		}
	}
	return ""
}

// pin reads the `plugin <hook> <command> <version> <digest>` pin on line, in
// either its single-line or its block form, or reports ok=false.
func (o outline) pin(line int) (pin pinLine, ok bool) {
	d, found := o.byLine[line]
	if !found || d.keyword != "plugin" || len(d.args) != 4 {
		return pinLine{}, false
	}
	return pinLine{
		command: d.args[1].text,
		version: d.args[2].text,
		digest:  d.args[3].text,
		start:   d.args[2].start,
		endCol:  d.args[3].end,
	}, true
}

// wordAt is the index of the argument the cursor touches, or -1. A cursor just
// past the last character of a word still counts as on it, the way editors
// place it after a click at the end of a word.
func (d directive) wordAt(character int) int {
	for i, w := range d.args {
		if character >= w.start && character <= w.end {
			return i
		}
	}
	return -1
}

// onKeyword reports whether the cursor touches the directive's own keyword.
func (d directive) onKeyword(character int) bool {
	return !d.inBlock && character >= d.kw.start && character <= d.kw.end
}
