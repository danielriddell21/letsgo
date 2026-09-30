package plan

import (
	"slices"
	"strings"
	"testing"
)

func TestActionStates(t *testing.T) {
	for _, tt := range []struct {
		name          string
		action        Action
		before, after string
	}{
		{"new asset", Action{Op: Add, Kind: KindAsset, Planned: "sha256:b"}, "", "sha256:b"},
		{"replaced asset", Action{Op: Change, Kind: KindAsset, Observed: "sha256:a", Planned: "sha256:b"}, "sha256:a", "sha256:b"},
		{"kept tap file", Action{Op: Keep, Kind: KindTap, Observed: "blob:x", Planned: "blob:x"}, "blob:x", "blob:x"},
		{"new release", Action{Op: Add, Kind: KindRelease}, "absent", "current"},
		{"edited release", Action{Op: Change, Kind: KindRelease}, "edited", "current"},
		{"kept release", Action{Op: Keep, Kind: KindRelease}, "current", "current"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.action.Before(); got != tt.before {
				t.Errorf("Before = %q, want %q", got, tt.before)
			}
			if got := tt.action.After(); got != tt.after {
				t.Errorf("After = %q, want %q", got, tt.after)
			}
		})
	}
}

func TestDrifted(t *testing.T) {
	saved := []Action{
		{Op: Add, Kind: KindRelease, Target: "v1"},
		{Op: Add, Kind: KindAsset, Target: "a.tgz", Planned: "sha256:a"},
		{Op: Change, Kind: KindTap, Target: "Formula/x.rb", Observed: "blob:old", Planned: "blob:new"},
	}

	for _, tt := range []struct {
		name    string
		current []Action
		want    []string
	}{
		{"nothing moved", []Action{
			{Op: Add, Kind: KindRelease, Target: "v1"},
			{Op: Add, Kind: KindAsset, Target: "a.tgz", Planned: "sha256:a"},
			{Op: Change, Kind: KindTap, Target: "Formula/x.rb", Observed: "blob:old", Planned: "blob:new"},
		}, nil},
		{"a half-finished apply", []Action{
			{Op: Keep, Kind: KindRelease, Target: "v1"},
			{Op: Keep, Kind: KindAsset, Target: "a.tgz", Observed: "sha256:a", Planned: "sha256:a"},
			{Op: Add, Kind: KindTap, Target: "Formula/x.rb", Observed: "blob:old", Planned: "blob:new"},
		}, nil},
		{"a hand edit", []Action{
			{Op: Change, Kind: KindTap, Target: "Formula/x.rb", Observed: "blob:hand", Planned: "blob:new"},
		}, []string{"tap Formula/x.rb"}},
		{"an asset uploaded by someone else", []Action{
			{Op: Change, Kind: KindAsset, Target: "a.tgz", Observed: "sha256:other", Planned: "sha256:a"},
		}, []string{"asset a.tgz"}},
		{"a release made by someone else", []Action{
			{Op: Change, Kind: KindRelease, Target: "v1"},
		}, []string{"release v1"}},
		{"a target no longer observed", nil, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, d := range Drifted(saved, tt.current) {
				got = append(got, string(d.Kind)+" "+d.Target)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Drifted = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDriftNamesWhatItFound(t *testing.T) {
	d := Drift{Kind: KindTap, Target: "Formula/x.rb", Found: "blob:hand", Was: "blob:old", Would: "blob:new"}
	for _, want := range []string{"tap Formula/x.rb", "blob:hand", "blob:old", "blob:new"} {
		if !strings.Contains(d.String(), want) {
			t.Errorf("%q missing %q", d.String(), want)
		}
	}
}

func TestPendingLeavesOutWhatIsKept(t *testing.T) {
	got := Pending([]Action{
		{Op: Add, Kind: KindAsset, Target: "a"},
		{Op: Keep, Kind: KindAsset, Target: "b"},
		{Op: Change, Kind: KindTap, Target: "c"},
	})
	if got[KindAsset]["a"] != Add || got[KindTap]["c"] != Change {
		t.Errorf("Pending = %v", got)
	}
	if _, ok := got[KindAsset]["b"]; ok {
		t.Errorf("a kept target is pending: %v", got)
	}
}
