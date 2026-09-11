// Command fixture is a deliberately boring program used to prove that letsgo
// produces byte-identical artifacts. It exercises the two things a release
// build has to get right: linker-injected version metadata, and enough real
// code that the compiler has something to be nondeterministic about.
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Printf("fixture %s (%s) built %s\n", version, commit, date)
		return
	}

	// Map iteration order is randomised at run time, which is a common source
	// of nondeterministic *output*. It must not produce a nondeterministic
	// *binary*, and having it here means we would notice if it did.
	counts := map[string]int{}
	for _, word := range strings.Fields(strings.Join(os.Args[1:], " ")) {
		counts[strings.ToLower(word)]++
	}

	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("%s\t%d\n", k, counts[k])
	}
}
