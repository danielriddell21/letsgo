package promote

import (
	"fmt"
	"sort"
	"strings"

	"github.com/danielriddell21/letsgo/internal/manifest"
)

// Compare checks a rebuilt manifest against the RC's own, and reports every
// way they may not legitimately differ (PR-10).
//
// Deliberately not checked, because it is what the version bump is expected
// to change or what a bare rebuild cannot reproduce: Name, SHA256,
// BinarySHA256, Size, BinarySize and LDFlags on each artifact (all version- or
// digest-derived), Source's archive name and digest, the SBOM filename,
// an Image's Reference/Tags/Digest (only its platform count is compared), and
// Gates/APIChanges (checks-derived metadata, not reproduced by a plain
// rebuild — see rebuild's own doc comment for why Analyse is left off).
func Compare(rc, stable *manifest.Manifest) []string {
	var diffs []string
	add := func(format string, args ...any) {
		diffs = append(diffs, fmt.Sprintf(format, args...))
	}

	if rc.Commit != stable.Commit {
		add("commit: %s vs %s", short(rc.Commit), short(stable.Commit))
	}
	if rc.SourceDateEpoch != stable.SourceDateEpoch {
		add("source_date_epoch: %d vs %d", rc.SourceDateEpoch, stable.SourceDateEpoch)
	}
	if rc.ModuleDir != stable.ModuleDir {
		add("module_dir: %q vs %q", rc.ModuleDir, stable.ModuleDir)
	}
	if rc.TagPrefix != stable.TagPrefix {
		add("tag_prefix: %q vs %q", rc.TagPrefix, stable.TagPrefix)
	}

	if rc.Builder.Go != stable.Builder.Go {
		add("go version: %s vs %s", rc.Builder.Go, stable.Builder.Go)
	}
	comparePlugins(rc.Builder.Plugins, stable.Builder.Plugins, add)

	if rc.Modules.GoSumSHA256 != stable.Modules.GoSumSHA256 {
		add("go.sum: %s vs %s", short(rc.Modules.GoSumSHA256), short(stable.Modules.GoSumSHA256))
	}
	compareModuleList(rc.Modules.List, stable.Modules.List, add)

	compareFeatures(rc.Features, stable.Features, add)

	compareArtifacts(rc.Artifacts, stable.Artifacts, add)

	return diffs
}

func comparePlugins(rc, stable []manifest.BuilderPlugin, add func(string, ...any)) {
	rcByHook := make(map[string]manifest.BuilderPlugin, len(rc))
	for _, p := range rc {
		rcByHook[p.Hook] = p
	}
	stableByHook := make(map[string]manifest.BuilderPlugin, len(stable))
	for _, p := range stable {
		stableByHook[p.Hook] = p
	}

	hooks := make(map[string]bool, len(rcByHook)+len(stableByHook))
	for h := range rcByHook {
		hooks[h] = true
	}
	for h := range stableByHook {
		hooks[h] = true
	}

	for _, hook := range sortedKeys(hooks) {
		a, inRC := rcByHook[hook]
		b, inStable := stableByHook[hook]
		switch {
		case inRC && !inStable:
			add("plugin %s (%s): ran for the RC but not the rebuild", a.Command, hook)
		case !inRC && inStable:
			add("plugin %s (%s): ran for the rebuild but not the RC", b.Command, hook)
		case a.Command != b.Command || a.Version != b.Version || a.Digest != b.Digest:
			add("plugin %s: %s %s (%s) vs %s %s (%s)",
				hook, a.Command, a.Version, short(a.Digest), b.Command, b.Version, short(b.Digest))
		}
	}
}

func compareModuleList(rc, stable []manifest.Module, add func(string, ...any)) {
	rcByPath := make(map[string]string, len(rc))
	for _, m := range rc {
		rcByPath[m.Path] = m.Version
	}
	stableByPath := make(map[string]string, len(stable))
	for _, m := range stable {
		stableByPath[m.Path] = m.Version
	}

	paths := make(map[string]bool, len(rcByPath)+len(stableByPath))
	for p := range rcByPath {
		paths[p] = true
	}
	for p := range stableByPath {
		paths[p] = true
	}

	for _, path := range sortedKeys(paths) {
		rcVersion, inRC := rcByPath[path]
		stableVersion, inStable := stableByPath[path]
		switch {
		case inRC && !inStable:
			add("module %s@%s is in the RC's build but not the rebuild's", path, rcVersion)
		case !inRC && inStable:
			add("module %s@%s is in the rebuild but not the RC's", path, stableVersion)
		case rcVersion != stableVersion:
			add("module %s: %s vs %s", path, rcVersion, stableVersion)
		}
	}
}

func compareFeatures(rc, stable *manifest.Features, add func(string, ...any)) {
	rcDisabled, rcRequired := featureLists(rc)
	stableDisabled, stableRequired := featureLists(stable)

	if !equalStrings(rcDisabled, stableDisabled) {
		add("disabled features: %s vs %s", describeList(rcDisabled), describeList(stableDisabled))
	}
	if !equalStrings(rcRequired, stableRequired) {
		add("required features: %s vs %s", describeList(rcRequired), describeList(stableRequired))
	}
}

func featureLists(f *manifest.Features) (disabled, required []string) {
	if f == nil {
		return nil, nil
	}
	return f.Disabled, f.Required
}

// compareArtifacts matches artifacts by target (OS, Arch, Variant) rather
// than by name: an archive's name carries the version, which is exactly what
// promotion changes, so matching on it would compare nothing.
func compareArtifacts(rc, stable []manifest.Artifact, add func(string, ...any)) {
	rcByTarget := indexByTarget(rc)
	stableByTarget := indexByTarget(stable)

	targets := make(map[string]bool, len(rcByTarget)+len(stableByTarget))
	for t := range rcByTarget {
		targets[t] = true
	}
	for t := range stableByTarget {
		targets[t] = true
	}

	for _, target := range sortedKeys(targets) {
		a, inRC := rcByTarget[target]
		b, inStable := stableByTarget[target]
		switch {
		case inRC && !inStable:
			add("target %s is in the RC's release but missing from the rebuild", target)
		case !inRC && inStable:
			add("target %s is in the rebuild but was not in the RC's release", target)
		default:
			compareOneArtifact(target, a, b, add)
		}
	}
}

func compareOneArtifact(target string, a, b manifest.Artifact, add func(string, ...any)) {
	if !equalStrings(a.BinaryNames(), b.BinaryNames()) {
		add("target %s: binaries %s vs %s", target, describeList(a.BinaryNames()), describeList(b.BinaryNames()))
	}
	if !equalStrings(a.Build.Flags, b.Build.Flags) {
		add("target %s: build flags %s vs %s", target, describeList(a.Build.Flags), describeList(b.Build.Flags))
	}
	if !equalEnv(a.Build.Env, b.Build.Env) {
		add("target %s: build env %v vs %v", target, a.Build.Env, b.Build.Env)
	}
}

func indexByTarget(artifacts []manifest.Artifact) map[string]manifest.Artifact {
	out := make(map[string]manifest.Artifact, len(artifacts))
	for _, a := range artifacts {
		out[targetKey(a)] = a
	}
	return out
}

func targetKey(a manifest.Artifact) string {
	if a.Variant == "" {
		return fmt.Sprintf("%s/%s", a.OS, a.Arch)
	}
	return fmt.Sprintf("%s/%s (%s)", a.OS, a.Arch, a.Variant)
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalEnv(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func describeList(list []string) string {
	if len(list) == 0 {
		return "none"
	}
	return strings.Join(list, ", ")
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
