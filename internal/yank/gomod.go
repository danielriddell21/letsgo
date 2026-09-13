// Package yank retracts a published release.
//
// Go module releases cannot be unpublished. The proxy is immutable by design,
// which is correct and also means a bad release is permanent. What Go does
// provide is the `retract` directive, supported since Go 1.16 and almost never
// used, because nothing automates it and the one thing people get wrong about
// it is invisible: a retraction only takes effect once it is itself published
// in a *later* version.
//
// So this package does the mechanical parts — the directive, the release
// notice, the Homebrew formula — and is emphatic about the sequencing, which
// is the part that decides whether the retraction works at all.
package yank

import (
	"fmt"
	"strings"

	"github.com/danielriddell21/letsgo/internal/semver"
)

// Retract adds a retract directive for version to a go.mod file's contents.
//
// The file is edited textually rather than reformatted. `go mod edit` would be
// the obvious tool, and it cannot attach the rationale comment — which is the
// part `go list -m -retracted` shows to whoever is about to depend on the bad
// release, and therefore the only part that explains anything.
func Retract(goMod []byte, version, reason string) ([]byte, bool, error) {
	if !strings.HasPrefix(version, "v") {
		return nil, false, fmt.Errorf("yank: %q is not a version; retract takes a tag such as v1.2.3", version)
	}
	if _, ok := semver.Parse(version); !ok {
		return nil, false, fmt.Errorf("yank: %q is not a semantic version", version)
	}

	parsed, _ := semver.Parse(version)

	lines := strings.Split(string(goMod), "\n")
	if retracted(lines, version) {
		return goMod, false, nil
	}

	entry := version
	if reason = strings.TrimSpace(reason); reason != "" {
		entry += " // " + oneLine(reason)
	}

	if at, indent, ok := blockInsertion(lines, parsed); ok {
		lines = insert(lines, at, indent+entry)
		return []byte(strings.Join(lines, "\n")), true, nil
	}
	if at, ok := afterLastDirective(lines); ok {
		lines = insert(lines, at, "retract "+entry)
		return []byte(strings.Join(lines, "\n")), true, nil
	}

	// No retract directive yet. A block rather than a single line, because a
	// project that retracts once tends to retract again, and the second entry
	// should not need a reshuffle.
	trimmed := strings.TrimRight(string(goMod), "\n")
	return []byte(trimmed + "\n\nretract (\n\t" + entry + "\n)\n"), true, nil
}

// retracted reports whether version is already retracted, so that running
// yank twice changes nothing the second time.
func retracted(lines []string, version string) bool {
	for _, line := range lines {
		fields := strings.Fields(stripComment(line))
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "retract" {
			fields = fields[1:]
		}
		for _, f := range fields {
			if f == version {
				return true
			}
		}
	}
	return false
}

// blockInsertion finds where version belongs inside an existing `retract (`
// block, keeping entries in version order.
func blockInsertion(lines []string, version semver.Version) (at int, indent string, ok bool) {
	inBlock := false

	for i, line := range lines {
		code := strings.TrimSpace(stripComment(line))

		if !inBlock {
			if code == "retract (" || strings.HasPrefix(code, "retract (") {
				inBlock = true
				at, indent, ok = i+1, "\t", true
			}
			continue
		}

		if code == ")" {
			return at, indent, ok
		}
		if code == "" {
			continue
		}

		// Entries stay ordered, so the block reads as a history rather than as
		// the order somebody happened to yank things in. An entry this cannot
		// parse — a retracted range, say — is left where it is and treated as
		// sorting before anything new.
		existing, parsable := semver.Parse(strings.Fields(code)[0])
		if !parsable || semver.Compare(existing, version) < 0 {
			at = i + 1
			indent = leadingSpace(line)
		}
	}
	return 0, "", false
}

// afterLastDirective finds the line after the last single-line retract, for a
// file that uses that form.
func afterLastDirective(lines []string) (int, bool) {
	at, found := 0, false
	for i, line := range lines {
		fields := strings.Fields(stripComment(line))
		if len(fields) >= 2 && fields[0] == "retract" {
			at, found = i+1, true
		}
	}
	return at, found
}

func insert(lines []string, at int, line string) []string {
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:at]...)
	out = append(out, line)
	return append(out, lines[at:]...)
}

func stripComment(line string) string {
	if i := strings.Index(line, "//"); i >= 0 {
		return line[:i]
	}
	return line
}

func leadingSpace(line string) string {
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}

// oneLine flattens a reason so it cannot break the file it is written into.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
