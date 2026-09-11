package gobuild

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"sync"
)

// ReleaseTargets is the default build matrix.
//
// Five targets covering the platforms a Go CLI is actually installed on.
// Deliberately not "every platform the toolchain can emit": each extra target
// costs build time and an asset nobody downloads, and a config file exists for
// projects that genuinely need more.
var ReleaseTargets = []Target{
	{OS: "linux", Arch: "amd64"},
	{OS: "linux", Arch: "arm64"},
	{OS: "darwin", Arch: "amd64"},
	{OS: "darwin", Arch: "arm64"},
	{OS: "windows", Arch: "amd64"},
}

// ParseTarget reads a "goos/goarch" pair.
func ParseTarget(s string) (Target, error) {
	goos, goarch, found := strings.Cut(strings.TrimSpace(s), "/")
	if !found {
		return Target{}, fmt.Errorf("gobuild: target %q is not in goos/goarch form", s)
	}
	goos, goarch = strings.TrimSpace(goos), strings.TrimSpace(goarch)
	if goos == "" || goarch == "" {
		return Target{}, fmt.Errorf("gobuild: target %q is not in goos/goarch form", s)
	}
	if strings.Contains(goarch, "/") {
		return Target{}, fmt.Errorf("gobuild: target %q has too many parts", s)
	}
	return Target{OS: goos, Arch: goarch}, nil
}

// ParseTargets reads a list of targets, rejecting duplicates.
func ParseTargets(list []string) ([]Target, error) {
	out := make([]Target, 0, len(list))
	for _, s := range list {
		target, err := ParseTarget(s)
		if err != nil {
			return nil, err
		}
		if slices.Contains(out, target) {
			return nil, fmt.Errorf("gobuild: target %s is listed more than once", target)
		}
		out = append(out, target)
	}
	return out, nil
}

var (
	supportedOnce sync.Once
	supportedList []Target
	supportedErr  error
)

// Supported returns every target the toolchain can build for, as reported by
// the toolchain itself rather than a list we would have to keep current.
func Supported(ctx context.Context, goBin string) ([]Target, error) {
	supportedOnce.Do(func() {
		if goBin == "" {
			goBin = "go"
		}
		out, err := exec.CommandContext(ctx, goBin, "tool", "dist", "list").Output()
		if err != nil {
			supportedErr = fmt.Errorf("gobuild: listing supported targets: %w", err)
			return
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if target, err := ParseTarget(line); err == nil {
				supportedList = append(supportedList, target)
			}
		}
	})
	return supportedList, supportedErr
}

// Validate reports any target the toolchain cannot build for.
//
// This runs during planning, so a typo costs two seconds rather than being
// discovered after the rest of the matrix has already been compiled.
func Validate(ctx context.Context, goBin string, targets []Target) error {
	if len(targets) == 0 {
		return fmt.Errorf("gobuild: no targets to build")
	}

	supported, err := Supported(ctx, goBin)
	if err != nil {
		return err
	}

	var unknown []Target
	for _, t := range targets {
		if !slices.Contains(supported, t) {
			unknown = append(unknown, t)
		}
	}
	if len(unknown) == 0 {
		return nil
	}

	var b strings.Builder
	for i, t := range unknown {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "gobuild: %s is not a target this toolchain can build", t)
		if near := nearestTarget(t, supported); near != "" {
			fmt.Fprintf(&b, " (did you mean %s?)", near)
		}
	}
	return fmt.Errorf("%s", b.String())
}

// nearestTarget suggests a target sharing one half of the pair, which covers
// the usual mistakes: darwin/x86_64 for darwin/amd64, or linux/aarch64 for
// linux/arm64.
func nearestTarget(want Target, supported []Target) string {
	var sameOS, sameArch []string
	for _, t := range supported {
		switch {
		case t.OS == want.OS:
			sameOS = append(sameOS, t.String())
		case t.Arch == want.Arch:
			sameArch = append(sameArch, t.String())
		}
	}

	// A recognised OS with an unrecognised architecture is the more common
	// mistake, and the more useful suggestion.
	candidates := sameOS
	if len(candidates) == 0 {
		candidates = sameArch
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.Strings(candidates)
	return candidates[0]
}
