package semver

import "testing"

func TestParse(t *testing.T) {
	good := map[string]Version{
		"v1.2.3":         {Major: 1, Minor: 2, Patch: 3},
		"1.2.3":          {Major: 1, Minor: 2, Patch: 3},
		"v0.0.0":         {},
		"v10.20.30":      {Major: 10, Minor: 20, Patch: 30},
		"v1.2.3-rc1":     {Major: 1, Minor: 2, Patch: 3, Prerelease: "rc1"},
		"v1.2.3+build.5": {Major: 1, Minor: 2, Patch: 3, Build: "build.5"},
		"v1.2.3-rc.1+b":  {Major: 1, Minor: 2, Patch: 3, Prerelease: "rc.1", Build: "b"},
	}
	for tag, want := range good {
		t.Run(tag, func(t *testing.T) {
			got, ok := Parse(tag)
			if !ok || got != want {
				t.Errorf("Parse(%q) = %+v, %v; want %+v", tag, got, ok, want)
			}
		})
	}

	bad := []string{"", "v", "1.2", "1.2.3.4", "va.b.c", "v1.2.x", "v01.2.3", "v1.-2.3", "latest"}
	for _, tag := range bad {
		t.Run("bad:"+tag, func(t *testing.T) {
			if _, ok := Parse(tag); ok {
				t.Errorf("Parse(%q) succeeded", tag)
			}
		})
	}
}

func TestCompare(t *testing.T) {
	// Each entry must sort before the next.
	ordered := []string{
		"v0.9.0", "v0.10.0", "v1.0.0-alpha", "v1.0.0-alpha.1", "v1.0.0-alpha.beta",
		"v1.0.0-beta", "v1.0.0-beta.2", "v1.0.0-beta.11", "v1.0.0-rc.1", "v1.0.0",
		"v1.0.1", "v1.1.0", "v2.0.0",
	}
	for i := 0; i+1 < len(ordered); i++ {
		a, _ := Parse(ordered[i])
		b, _ := Parse(ordered[i+1])
		if got := Compare(a, b); got != -1 {
			t.Errorf("Compare(%s, %s) = %d, want -1", ordered[i], ordered[i+1], got)
		}
		if got := Compare(b, a); got != 1 {
			t.Errorf("Compare(%s, %s) = %d, want 1", ordered[i+1], ordered[i], got)
		}
	}
	a, _ := Parse("v1.2.3")
	if Compare(a, a) != 0 {
		t.Error("a version does not equal itself")
	}
	// Build metadata is not part of precedence.
	b, _ := Parse("v1.2.3+other")
	if Compare(a, b) != 0 {
		t.Error("build metadata affected ordering")
	}
}

// Lexical comparison would put v0.10.0 before v0.9.0, which is the mistake
// this package exists to avoid.
func TestLatestPicksHighestNotLast(t *testing.T) {
	tags := []string{"v0.9.0", "v0.10.0", "v0.2.0", "not-a-version", "v0.10.0-rc1"}
	if got := Latest(tags); got != "v0.10.0" {
		t.Errorf("Latest = %q, want v0.10.0", got)
	}
	// A pre-release of a higher version still outranks a lower release: the
	// pre-release segment only breaks ties between equal numeric versions.
	if got := Latest(tags, "v0.10.0"); got != "v0.10.0-rc1" {
		t.Errorf("Latest excluding v0.10.0 = %q, want v0.10.0-rc1", got)
	}
	if got := Latest(tags, "v0.10.0", "v0.10.0-rc1"); got != "v0.9.0" {
		t.Errorf("Latest excluding both 0.10.0 tags = %q, want v0.9.0", got)
	}
	if got := Latest([]string{"nope", "also-nope"}); got != "" {
		t.Errorf("Latest with no versions = %q, want empty", got)
	}
}

func TestIsPrerelease(t *testing.T) {
	v, _ := Parse("v1.0.0-rc1")
	if !v.IsPrerelease() {
		t.Error("rc1 is a prerelease")
	}
	v, _ = Parse("v1.0.0+build")
	if v.IsPrerelease() {
		t.Error("build metadata is not a prerelease")
	}
}
