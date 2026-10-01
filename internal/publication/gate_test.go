package publication

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/feature"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/release"
	"github.com/danielriddell21/letsgo/internal/sumdb"
)

// Each way the cross-check is switched off returns before any network call,
// so the plan can be a literal and the source archive need not exist.
func TestCheckSumdbSkipsWithoutTouchingTheNetwork(t *testing.T) {
	t.Setenv("GOPRIVATE", "github.com/you/*")
	t.Setenv("GONOSUMDB", "")
	t.Setenv("GONOSUMCHECK", "")

	tests := map[string]*plan.Plan{
		"disabled":       {Config: &config.Config{}, Tag: "v1.0.0", Features: feature.Resolve([]string{"sumdb"})},
		"proxy warm off": {Config: &config.Config{}, Tag: "v1.0.0", Features: feature.Resolve([]string{"proxy-warm"})},
		"private module": {Config: &config.Config{}, Tag: "v1.0.0", Module: discover.Module{Path: "github.com/you/foo"}},
		"draft":          {Config: &config.Config{Draft: true}, Tag: "v1.0.0", Required: []string{"sumdb"}},
	}
	for name, p := range tests {
		t.Run(name, func(t *testing.T) {
			if err := checkSumdb(context.Background(), io.Discard, p, sumdbDecision(p, false), t.TempDir(), &release.Result{}); err != nil {
				t.Fatalf("checkSumdb = %v, want nil", err)
			}
		})
	}
}

// Not being able to reach the checksum database is a warning, unless sumdb is
// required, in which case a release nobody could check must not go out.
func TestCheckSumdbTreatsAnUnreachableDatabaseAsAWarningUnlessRequired(t *testing.T) {
	t.Setenv("GOPRIVATE", "")
	t.Setenv("GONOSUMDB", "")
	t.Setenv("GONOSUMCHECK", "")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer server.Close()
	old := sumdbURL
	sumdbURL = server.URL
	defer func() { sumdbURL = old }()

	tests := map[string]struct {
		required []string
		wantErr  bool
	}{
		"not required": {nil, false},
		"required":     {[]string{"sumdb"}, true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			p := &plan.Plan{
				Config:   &config.Config{},
				Module:   discover.Module{Path: "github.com/you/foo"},
				Version:  "1.0.0",
				Tag:      "v1.0.0",
				Proxy:    server.URL,
				Required: tt.required,
			}
			err := checkSumdb(context.Background(), io.Discard, p, sumdbDecision(p, false), t.TempDir(), &release.Result{})
			if (err != nil) != tt.wantErr {
				t.Fatalf("checkSumdb = %v, want error %v", err, tt.wantErr)
			}
		})
	}
}

// A snapshot, a draft and a `module <dir>` release never reach the network:
// there is nothing public to compare against.
func TestGateSkipsWhatIsNotPublic(t *testing.T) {
	p := &plan.Plan{Config: &config.Config{}, Features: feature.Resolve([]string{"proxy-warm", "sumdb"})}
	scoped := &plan.Plan{Config: &config.Config{ModuleDir: "web"}}

	tests := map[string]struct {
		p               *plan.Plan
		snapshot, draft bool
	}{
		"snapshot": {p, true, false},
		"draft":    {p, false, true},
		"scoped":   {scoped, false, false},
		"disabled": {p, false, false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			p := *tt.p
			cfg := *p.Config
			cfg.Draft = tt.draft
			p.Config = &cfg
			err := gate(context.Background(), io.Discard, Options{
				Plan: &p, Result: &release.Result{}, Dir: t.TempDir(), Snapshot: tt.snapshot,
			})
			if err != nil {
				t.Fatalf("gate = %v, want nil", err)
			}
		})
	}
}

// The gate stands in front of the release: a check that cannot be made when it
// is required stops the publication before anything reaches the forge.
func TestPublishStopsAtTheGateBeforeTheRelease(t *testing.T) {
	t.Setenv("GOPRIVATE", "")
	t.Setenv("GONOSUMDB", "")
	t.Setenv("GONOSUMCHECK", "")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer server.Close()
	old := sumdbURL
	sumdbURL = server.URL
	defer func() { sumdbURL = old }()

	o, forge, _ := publishFixture(t)
	o.Snapshot = false
	o.Plan.Module = discover.Module{Path: "github.com/you/foo"}
	o.Plan.Proxy, o.Plan.Required = server.URL, []string{"sumdb"}

	_, err := Publish(t.Context(), o)

	stopped, ok := errors.AsType[*StepError](err)
	if !ok || stopped.Step != StepGate || len(stopped.Done) != 0 {
		t.Fatalf("err = %v, want a StepError at %s with nothing done", err, StepGate)
	}
	if len(forge.created) != 0 {
		t.Errorf("created = %+v, want nothing released past a failed gate", forge.created)
	}
}

func TestReportSumdb(t *testing.T) {
	p := &plan.Plan{Module: discover.Module{Path: "github.com/you/foo"}, Version: "1.0.0", Tag: "v1.0.0"}

	tests := map[string]struct {
		result   sumdb.Result
		required bool
		wantErr  bool
	}{
		"agrees":                 {sumdb.Result{Matched: true}, false, false},
		"no record":              {sumdb.Result{NotFound: true}, false, false},
		"no record but required": {sumdb.Result{NotFound: true}, true, true},
		"files differ":           {sumdb.Result{Mismatched: []string{"a.go"}, Missing: []string{"b.go"}}, false, true},
		"hashes differ":          {sumdb.Result{SumH1: "h1:x", ZipH1: "h1:y"}, false, true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if err := reportSumdb(io.Discard, p, tt.result, tt.required); (err != nil) != tt.wantErr {
				t.Fatalf("reportSumdb = %v, want error %v", err, tt.wantErr)
			}
		})
	}
}
