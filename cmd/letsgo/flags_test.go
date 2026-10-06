package main

import (
	"strings"
	"testing"
)

func TestPermuteMovesFlagsAhead(t *testing.T) {
	tests := map[string]struct {
		in   []string
		want []string
	}{
		"already in order":   {[]string{"--reason", "x", "v1.2.3"}, []string{"--reason", "x", "v1.2.3"}},
		"flag after operand": {[]string{"v1.2.3", "--reason", "x"}, []string{"--reason", "x", "v1.2.3"}},
		"boolean after":      {[]string{"v1.2.3", "--yes"}, []string{"--yes", "v1.2.3"}},
		"attached value":     {[]string{"v1.2.3", "--reason=x"}, []string{"--reason=x", "v1.2.3"}},
		"single dash":        {[]string{"v1.2.3", "-yes"}, []string{"-yes", "v1.2.3"}},
		"two operands":       {[]string{"a.json", "b.json"}, []string{"a.json", "b.json"}},
		"interleaved":        {[]string{"a", "--yes", "b", "--reason", "x"}, []string{"--yes", "--reason", "x", "a", "b"}},
		"nothing":            {nil, nil},
	}

	for name, c := range tests {
		t.Run(name, func(t *testing.T) {
			got := permute(flagSet(), c.in)
			if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
				t.Errorf("permute(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// A boolean flag must not swallow the argument after it, or `letsgo yank --yes
// v1.2.3` would lose its tag.
func TestPermuteKeepsBooleansFromEatingOperands(t *testing.T) {
	fs := flagSet()
	if err := fs.Parse(permute(fs, []string{"--yes", "v1.2.3"})); err != nil {
		t.Fatal(err)
	}
	if fs.NArg() != 1 || fs.Arg(0) != "v1.2.3" {
		t.Errorf("args = %v", fs.Args())
	}
}

// Everything after "--" is an operand by definition, however it is spelled.
func TestPermuteRespectsTheTerminator(t *testing.T) {
	got := permute(flagSet(), []string{"--yes", "--", "--not-a-flag", "x"})

	want := []string{"--yes", "--", "--not-a-flag", "x"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("permute = %v, want %v", got, want)
	}
}

// An unknown flag must reach the flag package to be reported, rather than
// quietly consuming the operand behind it.
func TestPermuteLeavesUnknownFlagsAlone(t *testing.T) {
	fs := flagSet()
	fs.SetOutput(discard{})

	if err := fs.Parse(permute(fs, []string{"v1.2.3", "--nonsense"})); err == nil {
		t.Fatal("an unknown flag was accepted")
	}
}

func TestErrUsage(t *testing.T) {
	if err := errUsage("letsgo yank <tag>"); !strings.HasPrefix(err.Error(), "usage: ") {
		t.Errorf("errUsage = %v", err)
	}
}
