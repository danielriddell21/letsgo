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
