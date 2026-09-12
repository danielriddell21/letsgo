package gate

import (
	"strings"
	"testing"
)

const apidiffReport = `Incompatible changes:
- (*Client).Do: removed
- New: changed from func(string) *Client to func(string, ...Option) *Client
Compatible changes:
- WithTimeout: added
- (*Client).Close: added
`

func TestParseAPIDiff(t *testing.T) {
	changes := parseAPIDiff(apidiffReport)

	if len(changes) != 4 {
		t.Fatalf("got %d changes, want 4: %+v", len(changes), changes)
	}

	incompatible := Incompatibles(changes)
	if len(incompatible) != 2 {
		t.Fatalf("got %d incompatible changes, want 2: %+v", len(incompatible), incompatible)
	}
	if incompatible[0].Text != "(*Client).Do: removed" {
		t.Errorf("first incompatible = %q", incompatible[0].Text)
	}

	// An addition cannot break a dependant, so it must not be counted as one.
	for _, c := range changes {
		if c.Kind == Incompatible && strings.Contains(c.Text, "added") {
			t.Errorf("an addition was classified as incompatible: %q", c.Text)
		}
	}
}

func TestParseAPIDiffHandlesNoChanges(t *testing.T) {
	for _, out := range []string{"", "\n", "Nothing to report\n"} {
		if got := parseAPIDiff(out); len(got) != 0 {
			t.Errorf("parseAPIDiff(%q) = %+v, want none", out, got)
		}
	}
}

func TestParseAPIDiffIgnoresCompatibleOnlyReports(t *testing.T) {
	changes := parseAPIDiff("Compatible changes:\n- Thing: added\n")
	if len(Incompatibles(changes)) != 0 {
		t.Error("a compatible-only report produced incompatible changes")
	}
	if len(changes) != 1 {
		t.Errorf("got %d changes, want 1", len(changes))
	}
}

func TestRequiredBump(t *testing.T) {
	tests := map[string]struct {
		changes []Change
		want    string
	}{
		"nothing changed":       {nil, "patch"},
		"additions only":        {[]Change{{Kind: Compatible}}, "minor"},
		"a removal":             {[]Change{{Kind: Incompatible}}, "major"},
		"additions and removal": {[]Change{{Kind: Compatible}, {Kind: Incompatible}}, "major"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := RequiredBump(tt.changes); got != tt.want {
				t.Errorf("RequiredBump = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestChangeString(t *testing.T) {
	c := Change{Package: "example.com/foo", Text: "Bar: removed"}
	if got := c.String(); got != "example.com/foo: Bar: removed" {
		t.Errorf("String() = %q", got)
	}
	if got := (Change{Text: "Bar: removed"}).String(); got != "Bar: removed" {
		t.Errorf("String() without a package = %q", got)
	}
}
