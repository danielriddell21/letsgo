package plan_test

import (
	"fmt"

	"github.com/danielriddell21/letsgo/plan"
)

// A companion tool reads a saved plan with Decode, or Read for a path, and
// works from the actions it lists. Nothing here needs the rest of letsgo.
func ExampleDecode() {
	file, err := plan.Decode([]byte(`{
		"schema": 1,
		"kind": "release",
		"repo": "you/demo",
		"tag": "v1.0.0",
		"commit": "abc123",
		"manifest_sha256": "sha256:00",
		"actions": [
			{"op": "+", "kind": "asset", "target": "demo.tar.gz", "planned": "sha256:ab12cd34ef56ab12"},
			{"op": "=", "kind": "proxy", "target": "example.com/demo", "observed": "v1.0.0", "planned": "v1.0.0"}
		]
	}`))
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(file.Tag, file.Changes())
	fmt.Print(plan.Markdown("letsgo plan", file.Actions))
	// Output:
	// v1.0.0 true
	// ## letsgo plan
	//
	// ```diff
	// + asset  demo.tar.gz  sha256:ab12…
	// ```
	//
	// Plan: 1 to add, 0 to change, 0 to remove. 1 unchanged.
}
