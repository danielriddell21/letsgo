package main

import (
	"context"
	"testing"

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
