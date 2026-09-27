package plugin

// KnownPlugin describes one of the plugins letsgo itself publishes: the hook
// it answers, and what it does.
//
// Hook is empty for a plugin that does not run through a hook at all — it
// reads a finished release rather than taking part in one, so there is
// nothing to pin.
type KnownPlugin struct {
	Command string
	Hook    Hook
	Summary string
}

// Known is the catalogue of first-party plugins.
//
// It exists only to help: filling in a pin, listing what is available, and
// naming a config that pins one to the wrong hook. It is never consulted to
// decide what runs — that stays exactly what letsgo.mod pins — so a plugin
// that is not in this list works exactly as one that is.
var Known = []KnownPlugin{
	{
		Command: "letsgo-multi",
		Hook:    HookArchiveLayout,
		Summary: "groups a module's commands into one archive per target",
	},
	{
		Command: "letsgo-env",
		Hook:    HookLDFlags,
		Summary: "compiles values from the environment into the binary",
	},
	{
		Command: "letsgo-cask",
		Summary: "writes a Homebrew cask from a published release",
	},
}

// Lookup finds a first-party plugin by its command name.
func Lookup(command string) (KnownPlugin, bool) {
	for _, k := range Known {
		if k.Command == command {
			return k, true
		}
	}
	return KnownPlugin{}, false
}
