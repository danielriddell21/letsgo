package plugin

import "testing"

func TestLookupFindsAKnownPlugin(t *testing.T) {
	got, ok := Lookup("letsgo-multi")
	if !ok || got.Hook != HookArchiveLayout {
		t.Errorf("Lookup(letsgo-multi) = %+v, %v", got, ok)
	}
}

func TestLookupMissesAnUnknownPlugin(t *testing.T) {
	if _, ok := Lookup("letsgo-third-party"); ok {
		t.Error("an unlisted plugin should not be found")
	}
}

// letsgo-cask answers tap-files: what else belongs in the tap beside the
// formula core writes on its own.
func TestLookupFindsCasksHook(t *testing.T) {
	got, ok := Lookup("letsgo-cask")
	if !ok || got.Hook != HookTapFiles {
		t.Errorf("Lookup(letsgo-cask) = %+v, %v, want %s", got, ok, HookTapFiles)
	}
}

func TestShortName(t *testing.T) {
	for _, c := range []struct{ command, want string }{
		{"letsgo-env", "env"},
		{"letsgo-cask", "cask"},
		{"third-party", "third-party"},
	} {
		if got := ShortName(c.command); got != c.want {
			t.Errorf("ShortName(%q) = %q, want %q", c.command, got, c.want)
		}
	}
}

// A pin by path is the same plugin as one by name: its catalogue entry, its
// config file and its probe all follow the file's name.
func TestPinnedByPathIsLookedUpByName(t *testing.T) {
	for _, command := range []string{"./bin/letsgo-docker", "/opt/letsgo/letsgo-docker", `tools\letsgo-docker.exe`} {
		got, ok := Lookup(command)
		if !ok || got.Command != "letsgo-docker" {
			t.Errorf("Lookup(%q) = %+v, %v", command, got, ok)
		}
		if short := ShortName(command); short != "docker" {
			t.Errorf("ShortName(%q) = %q, want docker", command, short)
		}
	}
}
