package suggest

import "testing"

func TestNearest(t *testing.T) {
	directives := []string{"project", "module", "build", "tags", "changelog", "sbom", "release", "plugin"}

	tests := []struct {
		name string
		want string
		in   []string
		out  string
	}{
		{"case only", "Build", directives, "build"},
		{"prefix", "buil", directives, "build"},
		{"transposed letters", "sbmo", directives, "sbom"},
		{"transposed in a long word", "chnagelog", directives, "changelog"},
		{"one edit in a short word", "tagz", directives, "tags"},
		{"too far for a short word", "tgz", directives, ""},
		{"nothing close", "zzzzzzzz", directives, ""},
		{"exact match is never suggested", "build", directives, ""},
		{"exact but case-differing candidates still suggest the other", "Main", []string{"Main", "main"}, "main"},
		{"empty want", "", directives, ""},
		{"no candidates", "build", nil, ""},
		{"ties go to the first", "xuild", []string{"build", "guild"}, "build"},
		{"unicode counts runes, not bytes", "héllo", []string{"hello"}, "hello"},
		{"long word within budget", "deploymentt", []string{"deployment"}, "deployment"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Nearest(tt.want, tt.in); got != tt.out {
				t.Errorf("Nearest(%q) = %q, want %q", tt.want, got, tt.out)
			}
		})
	}
}

func TestDistance(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want int
	}{
		{"", "", 0}, {"a", "", 1}, {"", "abc", 3}, {"kitten", "sitting", 3},
		{"same", "same", 0}, {"ab", "ba", 1}, {"abcd", "badc", 2}, {"héllo", "hello", 1},
	} {
		if got := distance(tt.a, tt.b); got != tt.want {
			t.Errorf("distance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
