package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/feature"
	"github.com/danielriddell21/letsgo/internal/plan"
	"github.com/danielriddell21/letsgo/internal/release"
)

// Each way the cross-check is switched off returns before any network call,
// so the plan can be a literal and the source archive need not exist.
func TestCheckSumdbSkipsWithoutTouchingTheNetwork(t *testing.T) {
	t.Setenv("GOPRIVATE", "github.com/you/*")
	t.Setenv("GONOSUMDB", "")
	t.Setenv("GONOSUMCHECK", "")

	tests := map[string]*plan.Plan{
		"disabled":       {Features: feature.Resolve([]string{"sumdb"})},
		"proxy warm off": {Features: feature.Resolve([]string{"proxy-warm"})},
		"private module": {Module: discover.Module{Path: "github.com/you/foo"}},
	}
	for name, p := range tests {
		t.Run(name, func(t *testing.T) {
			if err := checkSumdb(context.Background(), p, t.TempDir(), &release.Result{}); err != nil {
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
				Module:   discover.Module{Path: "github.com/you/foo"},
				Version:  "1.0.0",
				Tag:      "v1.0.0",
				Proxy:    server.URL,
				Required: tt.required,
			}
			err := checkSumdb(context.Background(), p, t.TempDir(), &release.Result{})
			if (err != nil) != tt.wantErr {
				t.Fatalf("checkSumdb = %v, want error %v", err, tt.wantErr)
			}
		})
	}
}

// A snapshot, a draft and a `module <dir>` release never reach the network:
// there is nothing public to compare against.
func TestWarmProxyAndCheckSumdbSkipsWhatIsNotPublic(t *testing.T) {
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
			err := warmProxyAndCheckSumdb(context.Background(), tt.p, t.TempDir(), &release.Result{}, tt.snapshot, tt.draft)
			if err != nil {
				t.Fatalf("warmProxyAndCheckSumdb = %v, want nil", err)
			}
		})
	}
}
