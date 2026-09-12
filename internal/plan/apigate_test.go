package plan_test

import (
	"context"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/gate"
	"github.com/danielriddell21/letsgo/internal/plan"
)

// library is a module with an exported package, tagged twice: the second tag
// changes its API in the way given.
func library(t *testing.T, firstAPI, secondAPI, secondTag string) *repo {
	t.Helper()

	r := newRepo(t)
	r.write("go.mod", "module example.com/lib\n\ngo 1.24\n")
	r.write(".gitattributes", "* -text\n")
	r.write("lib/lib.go", firstAPI)
	r.commit("v1.0.0")

	r.write("lib/lib.go", secondAPI)
	r.commit(secondTag)
	return r
}

func requireAPIDiff(t *testing.T) {
	t.Helper()
	if _, err := gate.APIDiff(context.Background(), t.TempDir(), t.TempDir()); err != nil &&
		strings.Contains(err.Error(), "not installed") {
		t.Skip("apidiff is not installed")
	}
}

func apiCheck(t *testing.T, r *repo, allowBreaking bool) plan.Check {
	t.Helper()
	p, err := plan.Resolve(context.Background(), plan.Options{
		Dir: r.dir, Analyse: true, AllowBreaking: allowBreaking,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return check(t, p, "api compatibility")
}

const withDo = `package lib

// Client does things.
type Client struct{}

// Do performs a request.
func (c *Client) Do(path string) error { return nil }
`

const withoutDo = `package lib

// Client does things.
type Client struct{}
`

const withExtra = withDo + `
// Close releases resources.
func (c *Client) Close() error { return nil }
`

// Removing an exported method breaks every dependant at compile time. A minor
// bump promises that will not happen.
func TestAPIGateBlocksABreakingMinorRelease(t *testing.T) {
	requireAPIDiff(t)
	r := library(t, withDo, withoutDo, "v1.1.0")

	c := apiCheck(t, r, false)
	if c.Status != plan.Fail {
		t.Fatalf("api compatibility = %+v, want fail", c)
	}
	for _, want := range []string{"Do", "major version", "--allow-breaking"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail is missing %q:\n%s", want, c.Detail)
		}
	}
}

// The same change in a major release is exactly what a major release is for.
func TestAPIGateAllowsABreakingMajorRelease(t *testing.T) {
	requireAPIDiff(t)
	r := library(t, withDo, withoutDo, "v2.0.0")

	// A v2 tag also needs a /v2 module path, which is a different gate; this
	// one only asks whether the bump matches the API.
	if c := apiCheck(t, r, false); c.Status != plan.Pass {
		t.Errorf("api compatibility = %+v, want pass for a major release", c)
	}
}

// Additions cannot break anyone, so a minor release carrying only additions
// is correct.
func TestAPIGateAllowsAdditionsInAMinorRelease(t *testing.T) {
	requireAPIDiff(t)
	r := library(t, withDo, withExtra, "v1.1.0")

	if c := apiCheck(t, r, false); c.Status != plan.Pass {
		t.Errorf("api compatibility = %+v, want pass for additions", c)
	}
}

// The override exists, and using it must be visible rather than silent.
func TestAPIGateOverrideWarnsRatherThanPasses(t *testing.T) {
	requireAPIDiff(t)
	r := library(t, withDo, withoutDo, "v1.1.0")

	c := apiCheck(t, r, true)
	if c.Status != plan.Warn {
		t.Errorf("api compatibility = %+v, want warn", c)
	}
	if !strings.Contains(c.Detail, "--allow-breaking") {
		t.Errorf("the override is not recorded in the detail:\n%s", c.Detail)
	}
}

// The delta is kept on the plan so the changelog can describe what the code
// did rather than what a commit message claimed.
func TestAPIChangesReachThePlan(t *testing.T) {
	requireAPIDiff(t)
	r := library(t, withDo, withExtra, "v1.1.0")

	p, err := plan.Resolve(context.Background(), plan.Options{Dir: r.dir, Analyse: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.APIChanges) == 0 {
		t.Fatal("no API changes recorded on the plan")
	}
	found := false
	for _, c := range p.APIChanges {
		if strings.Contains(c.Text, "Close") {
			found = true
		}
	}
	if !found {
		t.Errorf("the added method is missing: %+v", p.APIChanges)
	}
}

// A first release has nothing to compare against.
func TestAPIGateSkipsAFirstRelease(t *testing.T) {
	r := newRepo(t)
	r.write("go.mod", "module example.com/lib\n\ngo 1.24\n")
	r.write("lib/lib.go", withDo)
	r.commit("v1.0.0")

	if c := apiCheck(t, r, false); c.Status != plan.Skip {
		t.Errorf("api compatibility = %+v, want skip", c)
	}
}
