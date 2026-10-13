package plan

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	pub "github.com/danielriddell21/letsgo/plugin"
)

// Without an override, a program is looked for in the system directories
// only, and a miss says which variable would name it.
func TestProbeProgramNamesTheOverrideWhenNothingIsFound(t *testing.T) {
	t.Setenv("LETSGO_TEST_TOOL", "")
	_, err := probeProgram("letsgo-no-such-tool", "LETSGO_TEST_TOOL")
	if err == nil || !strings.Contains(err.Error(), "set LETSGO_TEST_TOOL") {
		t.Errorf("err = %v", err)
	}
	if _, err := probeProgram("letsgo-no-such-tool", ""); err == nil {
		t.Error("a program in no system directory was found")
	}
}

// A probe that fails without a word still says which program it ran.
func TestProbeReportsASilentFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake tool is a shell script")
	}
	tool := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 3\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LETSGO_TEST_TOOL", tool)

	err := probe(context.Background(), []string{"tool", "version"}, "LETSGO_TEST_TOOL")
	if err == nil || !strings.Contains(err.Error(), tool) {
		t.Errorf("err = %v, want one naming %s", err, tool)
	}
}

func TestUnplatformedImage(t *testing.T) {
	line := func(args ...string) pub.ConfigLine { return pub.ConfigLine{Keyword: "image", Args: args} }

	for _, tc := range []struct {
		name  string
		lines []pub.ConfigLine
		want  bool
	}{
		{"none", nil, false},
		{"every image has one", []pub.ConfigLine{line("a", "platform", "linux/amd64"), line("a", "suffix", "-a")}, false},
		{"one does not", []pub.ConfigLine{line("a", "platform", "linux/amd64"), line("b", "suffix", "-b")}, true},
		{"a timeout is not an image", []pub.ConfigLine{{Keyword: "timeout", Args: []string{"1m"}}}, false},
	} {
		if got := unplatformedImage(&pub.Config{Lines: tc.lines}); got != tc.want {
			t.Errorf("%s: unplatformedImage = %v, want %v", tc.name, got, tc.want)
		}
	}
}
