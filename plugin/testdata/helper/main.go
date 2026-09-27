// Command helper is a minimal plugin built against Main, for
// plugin_test.go's TestMain* tests: they need a real process whose whole
// main is a call to Main, since Main's job — checking os.Args, then calling
// os.Exit on failure — can't be exercised in-process without ending the
// test binary itself.
package main

import "github.com/danielriddell21/letsgo/plugin"

func main() {
	plugin.Main(plugin.HookArchiveLayout, func(in plugin.ArchiveLayoutInput) (plugin.ArchiveLayoutOutput, error) {
		return plugin.ArchiveLayoutOutput{Archives: []plugin.OutputArchive{{Name: in.Project}}}, nil
	})
}
