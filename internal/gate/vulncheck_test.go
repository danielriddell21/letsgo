package gate

import (
	"errors"
	"strings"
	"testing"
)

// govulncheck emits a stream of objects, only some of which are findings.
const sampleOutput = `
{"config":{"protocol_version":"v1.0.0","scanner_name":"govulncheck"}}
{"progress":{"message":"Scanning your code..."}}
{"osv":{"id":"GO-2024-0001","summary":"something bad"}}
{"finding":{"osv":"GO-2024-0001","fixed_version":"v0.23.0","trace":[{"module":"golang.org/x/net"}]}}
{"finding":{"osv":"GO-2024-0001","fixed_version":"v0.23.0","trace":[{"module":"golang.org/x/net","package":"golang.org/x/net/http2","function":"readFrame"}]}}
{"finding":{"osv":"GO-2024-0002","fixed_version":"v1.2.0","trace":[{"module":"example.com/dep","package":"example.com/dep","function":"Parse","receiver":"Decoder"}]}}
{"finding":{"osv":"GO-2024-0003","trace":[{"module":"example.com/unused","package":"example.com/unused"}]}}
`

func TestParseVulncheckReportsOnlyReachableFindings(t *testing.T) {
	got, err := parseVulncheck(strings.NewReader(sampleOutput))
	if err != nil {
		t.Fatalf("parseVulncheck: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d vulnerabilities, want 2: %+v", len(got), got)
	}

	// Sorted, so the order is stable for reporting.
	if got[0].ID != "GO-2024-0001" || got[1].ID != "GO-2024-0002" {
		t.Errorf("ids = %s, %s", got[0].ID, got[1].ID)
	}
	// A module in the graph whose vulnerable code is never called is not a
	// reason to block a release.
	for _, v := range got {
		if v.ID == "GO-2024-0003" {
			t.Error("an unreachable finding was reported")
		}
	}
	if got[0].Symbol != "golang.org/x/net/http2.readFrame" {
		t.Errorf("symbol = %q", got[0].Symbol)
	}
	if got[1].Symbol != "example.com/dep.Decoder.Parse" {
		t.Errorf("symbol with receiver = %q", got[1].Symbol)
	}
	if got[0].FixedIn != "v0.23.0" {
		t.Errorf("FixedIn = %q", got[0].FixedIn)
	}
}

// The same advisory reached by several paths is one problem to fix.
func TestParseVulncheckDeduplicates(t *testing.T) {
	const repeated = `
{"finding":{"osv":"GO-1","trace":[{"package":"p","function":"A"}]}}
{"finding":{"osv":"GO-1","trace":[{"package":"p","function":"B"}]}}
{"finding":{"osv":"GO-1","trace":[{"package":"p","function":"C"}]}}
`
	got, err := parseVulncheck(strings.NewReader(repeated))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("got %d findings for one advisory", len(got))
	}
}

func TestParseVulncheckHandlesCleanOutput(t *testing.T) {
	got, err := parseVulncheck(strings.NewReader(`{"config":{}}` + "\n" + `{"progress":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}
}

func TestParseVulncheckRejectsGarbage(t *testing.T) {
	if _, err := parseVulncheck(strings.NewReader("not json")); err == nil {
		t.Error("garbage was accepted")
	}
}

func TestVulnerabilityString(t *testing.T) {
	v := Vulnerability{ID: "GO-2024-0001", Symbol: "pkg.Fn", FixedIn: "v1.2.3"}
	s := v.String()
	for _, want := range []string{"GO-2024-0001", "pkg.Fn", "v1.2.3"} {
		if !strings.Contains(s, want) {
			t.Errorf("%q is missing %q", s, want)
		}
	}
}

// An absent tool is a gate that did not run, which is not the same as one
// that passed.
func TestMissingToolIsDistinguishable(t *testing.T) {
	err := &MissingToolError{Tool: "govulncheck", Install: VulncheckInstall}
	if !errors.Is(err, ErrToolMissing) {
		t.Error("a missing tool does not unwrap to ErrToolMissing")
	}
	if !strings.Contains(err.Error(), "go install") {
		t.Errorf("the error does not say how to fix it: %v", err)
	}
}
