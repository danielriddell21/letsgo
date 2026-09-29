package bump

import (
	"strings"
	"testing"
)

// JSON is Proposal's wire form for `letsgo tag --json`: the same evidence
// reportProposal writes as text, schema-versioned.
func TestProposalJSON(t *testing.T) {
	p := Proposal{
		Previous: "1.2.3",
		Next:     "v1.3.0",
		Signals: []Signal{
			{Source: "commits", Level: Minor, Detail: "a feature commit"},
			{Source: "api", Level: Patch, Detail: "an addition"},
		},
		Notes: []string{"v2 needs the module path to end /v2 first"},
	}
	p.Level = Minor

	data, err := p.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	out := string(data)

	for _, want := range []string{
		`"schema": 1`,
		`"previous": "1.2.3"`,
		`"next": "v1.3.0"`,
		`"level": "minor"`,
		`"disagree": true`,
		`"source": "commits"`,
		`"detail": "a feature commit"`,
		`"notes": [`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("JSON() = %s, want it to contain %q", out, want)
		}
	}
}

func TestProposalJSONOmitsEmptyOptionalFields(t *testing.T) {
	p := Proposal{Next: "v0.1.0"}

	data, err := p.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	out := string(data)

	for _, absent := range []string{`"previous"`, `"disagree"`, `"signals"`, `"notes"`} {
		if strings.Contains(out, absent) {
			t.Errorf("JSON() = %s, want no %q field when empty", out, absent)
		}
	}
}
