package lsp

import "testing"

func TestOutlineLocatesWords(t *testing.T) {
	o := outlineOf("letsgo.mod", "build linux/amd64 // why\nplugin (\n\tldflags x\n)\n")

	d := o.byLine[0]
	if d.keyword != "build" || d.inBlock || d.kw != (word{"build", 0, 5}) {
		t.Errorf("line 0 = %+v", d)
	}
	if len(d.args) != 1 || d.args[0] != (word{"linux/amd64", 6, 17}) {
		t.Errorf("line 0 args = %+v", d.args)
	}

	inner := o.byLine[2]
	if inner.keyword != "plugin" || !inner.inBlock || len(inner.args) != 2 || inner.args[0] != (word{"ldflags", 1, 8}) {
		t.Errorf("line 2 = %+v", inner)
	}
	if _, ok := o.byLine[3]; ok {
		t.Error("the closing parenthesis is a directive")
	}
}

func TestOutlineKeepsWhatItCanReadOfABrokenFile(t *testing.T) {
	o := outlineOf("letsgo.mod", "project foo\n)\nbuild linux/amd64\n")
	if _, ok := o.byLine[2]; !ok {
		t.Error("a stray parenthesis hid the line after it")
	}
	if got := outlineOf("letsgo.mod", "").blockAt(0); got != "" {
		t.Errorf("empty document: blockAt = %q", got)
	}
}

func TestWordAt(t *testing.T) {
	d := outlineOf("letsgo.mod", "build linux/amd64").byLine[0]
	tests := []struct {
		character, want int
		keyword         bool
	}{{0, -1, true}, {5, -1, true}, {6, 0, false}, {17, 0, false}, {18, -1, false}}
	for _, tt := range tests {
		if got := d.wordAt(tt.character); got != tt.want || d.onKeyword(tt.character) != tt.keyword {
			t.Errorf("character %d: wordAt = %d, onKeyword = %v; want %d, %v", tt.character, got, d.onKeyword(tt.character), tt.want, tt.keyword)
		}
	}
}
