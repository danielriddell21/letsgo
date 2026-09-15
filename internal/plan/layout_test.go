package plan

import (
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/discover"
	"github.com/danielriddell21/letsgo/internal/plugin"
)

// A layout plugin that drops a command ships a release missing a binary, and
// one that repeats a command produces two archives claiming the same program.
// Both are silent, so the answer is checked rather than trusted.
func TestLayoutGroupsRejectsAnIncompleteAnswer(t *testing.T) {
	commands := []discover.MainPackage{
		{RelPath: "./cmd/alpha", BinaryName: "alpha"},
		{RelPath: "./cmd/beta", BinaryName: "beta"},
	}

	tests := []struct {
		name string
		out  plugin.ArchiveLayoutOutput
		want string
	}{
		{
			"a command left out",
			plugin.ArchiveLayoutOutput{Archives: []plugin.OutputArchive{
				{Name: "tools", Binaries: []string{"alpha"}},
			}},
			"beta is in no archive",
		},
		{
			"a command in two archives",
			plugin.ArchiveLayoutOutput{Archives: []plugin.OutputArchive{
				{Name: "one", Binaries: []string{"alpha", "beta"}},
				{Name: "two", Binaries: []string{"alpha"}},
			}},
			"alpha is in both",
		},
		{
			"a binary the module does not build",
			plugin.ArchiveLayoutOutput{Archives: []plugin.OutputArchive{
				{Name: "tools", Binaries: []string{"alpha", "beta", "gamma"}},
			}},
			"which this module does not build",
		},
		{
			"an archive with no name",
			plugin.ArchiveLayoutOutput{Archives: []plugin.OutputArchive{
				{Binaries: []string{"alpha", "beta"}},
			}},
			"no name",
		},
		{
			"an empty archive",
			plugin.ArchiveLayoutOutput{Archives: []plugin.OutputArchive{
				{Name: "one", Binaries: []string{"alpha", "beta"}},
				{Name: "two"},
			}},
			"holds no binaries",
		},
	}

	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			_, err := layoutGroups(c.out, commands)
			if err == nil {
				t.Fatal("the layout should have been refused")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want it to mention %q", err, c.want)
			}
		})
	}
}

func TestLayoutGroupsAcceptsACompleteAnswer(t *testing.T) {
	commands := []discover.MainPackage{
		{RelPath: "./cmd/alpha", BinaryName: "alpha"},
		{RelPath: "./cmd/beta", BinaryName: "beta"},
	}

	groups, err := layoutGroups(plugin.ArchiveLayoutOutput{Archives: []plugin.OutputArchive{
		{Name: "tools", Binaries: []string{"alpha", "beta"}},
	}}, commands)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].Name != "tools" || len(groups[0].Commands) != 2 {
		t.Errorf("groups = %+v", groups)
	}
	if groups[0].Commands[0].RelPath != "./cmd/alpha" {
		t.Errorf("commands were not resolved to their packages: %+v", groups[0].Commands)
	}
}

// The note is how `plan --explain` says what a layout plugin decided, and it
// is the only place a reader sees that before the archives exist.
func TestDescribeGroupsNamesEachArchiveAndItsBinaries(t *testing.T) {
	groups := []Group{
		{Name: "toolshed", Commands: []discover.MainPackage{
			{BinaryName: "crabs"}, {BinaryName: "duck"},
		}},
		{Name: "toolshed-extras", Commands: []discover.MainPackage{{BinaryName: "fish"}}},
	}

	want := "toolshed (crabs, duck), toolshed-extras (fish)"
	if got := describeGroups(groups); got != want {
		t.Errorf("describeGroups() = %q, want %q", got, want)
	}
	if got := describeGroups(nil); got != "" {
		t.Errorf("describeGroups(nil) = %q, want empty", got)
	}
}

// The ldflags hook injects values; it does not configure the linker. A plugin
// that could pass arbitrary flags could change how a binary is linked rather
// than what is in it, and that is a wider promise than "the answer is
// recorded".
func TestInjectedSymbolsAcceptsOnlyAssignments(t *testing.T) {
	ok, err := injectedSymbols([]string{
		"-X", "example.com/m/internal/build.Token=abc",
		"-X=example.com/m/internal/build.Endpoint=https://example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "example.com/m/internal/build.Token,example.com/m/internal/build.Endpoint"
	if strings.Join(ok, ",") != want {
		t.Errorf("symbols = %q, want %q", ok, want)
	}

	for _, c := range []struct {
		name, want string
		flags      []string
	}{
		{"a linker flag", "may only return -X assignments", []string{"-linkmode", "external"}},
		{"a trailing -X", "trailing -X", []string{"-X"}},
		{"nothing assigned", "assigns nothing", []string{"-X", "example.com/m.Token"}},
		{"no package", "must name a package", []string{"-X", "Token=abc"}},
		{
			"a value with a space",
			"cannot be recorded",
			[]string{"-X", "example.com/m/internal/build.Token=two words"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := injectedSymbols(c.flags); err == nil {
				t.Fatalf("%q should have been refused", c.flags)
			} else if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want it to mention %q", err, c.want)
			}
		})
	}
}
