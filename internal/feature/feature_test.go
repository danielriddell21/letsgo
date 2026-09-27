package feature

import "testing"

// catalogue is the PBS's own table (docs/pbs/features.md), reproduced here so
// a change to All that drifts from the spec fails a test rather than a
// review.
var catalogue = map[string]struct {
	kind    Kind
	disable bool
	require bool
}{
	"reproducible":   {Integrity, false, false},
	"source":         {Integrity, false, false},
	"manifest":       {Integrity, false, false},
	"checksums":      {Integrity, false, false},
	"tag-check":      {Integrity, false, false},
	"module-path":    {Integrity, false, false},
	"vulncheck":      {Gate, true, true},
	"api-gate":       {Gate, true, true},
	"budget":         {Gate, false, false},
	"sbom":           {Output, true, false},
	"install-script": {Output, true, true},
	"changelog":      {Output, true, false},
	"proxy-warm":     {Publish, true, false},
	"brew":           {Publish, false, false},
	"image":          {Publish, false, false},
}

func TestCatalogueMatchesSpec(t *testing.T) {
	if len(All) != len(catalogue) {
		t.Fatalf("All has %d features, catalogue has %d", len(All), len(catalogue))
	}
	for _, f := range All {
		want, ok := catalogue[f.Name]
		if !ok {
			t.Errorf("%s: not in the PBS catalogue", f.Name)
			continue
		}
		if f.Kind != want.kind {
			t.Errorf("%s: kind = %s, want %s", f.Name, f.Kind, want.kind)
		}
		if f.Disable != want.disable {
			t.Errorf("%s: Disable = %v, want %v", f.Name, f.Disable, want.disable)
		}
		if f.Require != want.require {
			t.Errorf("%s: Require = %v, want %v", f.Name, f.Require, want.require)
		}
	}
}

// TestIntegrityCannotBeDisabled holds ADR-0005's rule in code: integrity
// features never gain a Disable path, whatever the catalogue says.
func TestIntegrityCannotBeDisabled(t *testing.T) {
	for _, f := range All {
		if f.Kind == Integrity && f.Disable {
			t.Errorf("%s: an integrity feature must not be disable-able", f.Name)
		}
	}
}

// TestOffByDefaultHasEnableDirective: a feature nobody gets without asking
// must say what to write to get it, or `letsgo features` has nothing to
// point at.
func TestOffByDefaultHasEnableDirective(t *testing.T) {
	for _, f := range All {
		if !f.Default && f.Enable == "" {
			t.Errorf("%s: off by default but no Enable directive named", f.Name)
		}
		if f.Default && f.Enable != "" {
			t.Errorf("%s: on by default but names an Enable directive", f.Name)
		}
	}
}

func TestLookup(t *testing.T) {
	f, ok := Lookup("sbom")
	if !ok || f.Name != "sbom" {
		t.Fatalf("Lookup(%q) = %+v, %v", "sbom", f, ok)
	}
	if _, ok := Lookup("nope"); ok {
		t.Fatalf("Lookup(%q) found a feature that does not exist", "nope")
	}
}

func TestResolveAndOn(t *testing.T) {
	s := Resolve([]string{"sbom", "proxy-warm", "sbom"})

	if s.On("sbom") || s.On("proxy-warm") {
		t.Errorf("Set = %v, want sbom and proxy-warm off", s)
	}
	if !s.On("changelog") {
		t.Error("changelog should still be on")
	}
}

func TestResolveOfNothing(t *testing.T) {
	s := Resolve(nil)
	if len(s) != 0 {
		t.Errorf("Resolve(nil) = %v, want empty", s)
	}
	if !s.On("sbom") {
		t.Error("an empty Set should report every feature on")
	}
	if len(s.Disabled()) != 0 {
		t.Errorf("Disabled() = %v, want none", s.Disabled())
	}
}

// Disabled is sorted, so two runs that disabled the same features in a
// different order still record and print identically.
func TestSetDisabledIsSorted(t *testing.T) {
	s := Resolve([]string{"vulncheck", "api-gate", "sbom"})
	got := s.Disabled()
	want := []string{"api-gate", "sbom", "vulncheck"}

	if len(got) != len(want) {
		t.Fatalf("Disabled() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Disabled() = %v, want %v", got, want)
		}
	}
}

func TestKindString(t *testing.T) {
	cases := map[Kind]string{Integrity: "integrity", Gate: "gate", Output: "output", Publish: "publish"}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", k, got, want)
		}
	}
}
