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

// letsgo-cask reads a finished release; it has no hook to answer.
func TestKnownStandalonePluginHasNoHook(t *testing.T) {
	got, ok := Lookup("letsgo-cask")
	if !ok || got.Hook != "" {
		t.Errorf("Lookup(letsgo-cask) = %+v, %v, want an empty hook", got, ok)
	}
}
