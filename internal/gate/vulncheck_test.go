package gate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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

func TestParseVulncheckReportIncludesToolAndDBVersions(t *testing.T) {
	const withConfig = `
{"config":{"scanner_version":"v1.1.4","db_last_modified":"2026-09-23T00:00:00Z"}}
{"finding":{"osv":"GO-2024-0001","fixed_version":"v0.23.0","trace":[{"module":"golang.org/x/net","package":"golang.org/x/net/http2","function":"readFrame"}]}}
`
	report, err := parseVulncheckReport(strings.NewReader(withConfig))
	if err != nil {
		t.Fatal(err)
	}
	if report.GovulncheckVersion != "v1.1.4" {
		t.Errorf("GovulncheckVersion = %q", report.GovulncheckVersion)
	}
	if report.VulndbDate != "2026-09-23" {
		t.Errorf("VulndbDate = %q", report.VulndbDate)
	}
	if len(report.Vulnerabilities) != 1 || report.Vulnerabilities[0].Module != "golang.org/x/net" {
		t.Errorf("Vulnerabilities = %+v", report.Vulnerabilities)
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

// fakeVulncheck installs a govulncheck that logs its arguments and target to
// a file and reports a finding named for the GOOS it was run under.
func fakeVulncheck(t *testing.T) (log string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake tool is a shell script")
	}
	dir := t.TempDir()
	log = filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$* os=$GOOS arch=$GOARCH\" >> " + log + "\n" +
		`echo '{"config":{"scanner_version":"v1.1.4","db_last_modified":"2026-09-23T00:00:00Z"}}'` + "\n" +
		`echo "{\"finding\":{\"osv\":\"GO-$GOOS\",\"trace\":[{\"package\":\"p\",\"function\":\"F\"}]}}"` + "\n" +
		`echo '{"finding":{"osv":"GO-ALL","trace":[{"package":"p","function":"F"}]}}'` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "govulncheck"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOBIN", dir)
	return log
}

func TestVulncheckReportScansEachConfigurationAndMergesFindings(t *testing.T) {
	log := fakeVulncheck(t)

	report, err := VulncheckReport(context.Background(), nil, t.TempDir(),
		Scan{Tags: []string{"a", "b"}, Env: []string{"GOOS=linux", "GOARCH=amd64"}},
		Scan{Env: []string{"GOOS=windows", "GOARCH=arm64"}},
	)
	if err != nil {
		t.Fatalf("VulncheckReport: %v", err)
	}

	var ids []string
	for _, v := range report.Vulnerabilities {
		ids = append(ids, v.ID)
	}
	if got := strings.Join(ids, ","); got != "GO-ALL,GO-linux,GO-windows" {
		t.Errorf("findings = %s, want the union of both scans, once each", got)
	}
	if report.GovulncheckVersion != "v1.1.4" || report.VulndbDate != "2026-09-23" {
		t.Errorf("report = %+v", report)
	}

	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	want := "-format json -tags a,b ./... os=linux arch=amd64\n-format json ./... os=windows arch=arm64\n"
	if string(calls) != want {
		t.Errorf("govulncheck ran as:\n%s\nwant:\n%s", calls, want)
	}
}

func TestVulncheckReportWithoutScansRunsOnce(t *testing.T) {
	log := fakeVulncheck(t)

	if _, err := Vulncheck(context.Background(), nil, t.TempDir()); err != nil {
		t.Fatalf("Vulncheck: %v", err)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(calls), "\n"); n != 1 || !strings.HasPrefix(string(calls), "-format json ./...") {
		t.Errorf("calls = %q, want one plain run", calls)
	}
}
