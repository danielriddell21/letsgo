package plugin

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestHooksAreAClosedSet(t *testing.T) {
	if !HookLDFlags.Valid() || !HookArchiveLayout.Valid() {
		t.Error("a documented hook is not valid")
	}
	if Hook("exec").Valid() {
		t.Error("an undocumented hook is valid")
	}
}

func TestHooksListsBothHooks(t *testing.T) {
	if len(Hooks) != 2 || Hooks[0] != HookLDFlags || Hooks[1] != HookArchiveLayout {
		t.Errorf("Hooks = %v", Hooks)
	}
}

type testIn struct {
	Project string `json:"project"`
}

type testOut struct {
	Archive string `json:"archive"`
}

func layout(in testIn) (testOut, error) {
	return testOut{Archive: in.Project}, nil
}

// call runs run with the given arguments and stdin, and returns what it wrote
// to stdout. Both streams are swapped for pipes: the contract is what crosses
// them, so that is what the test drives.
func call(t *testing.T, args []string, stdin string) (string, error) {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer func() { _ = r.Close() }()

	input, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	if _, err := input.WriteString(stdin); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := input.Seek(0, 0); err != nil {
		t.Fatalf("seek: %v", err)
	}

	oldArgs, oldIn, oldOut := os.Args, os.Stdin, os.Stdout
	os.Args, os.Stdin, os.Stdout = args, input, w
	runErr := run(HookArchiveLayout, layout)
	os.Args, os.Stdin, os.Stdout = oldArgs, oldIn, oldOut

	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	var written strings.Builder
	buf := make([]byte, 1024)
	for {
		n, err := r.Read(buf)
		written.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return written.String(), runErr
}

func TestRunAnswersItsHook(t *testing.T) {
	stdout, err := call(t, []string{"letsgo-multi", "archive-layout"}, `{"project":"demo"}`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	var got testOut
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON object: %q", stdout)
	}
	if got.Archive != "demo" {
		t.Errorf("got %q", got.Archive)
	}
}

// A plugin asked for a hook it does not implement says so, rather than
// answering the wrong question with a plausible-looking result.
func TestRunRefusesAnotherHook(t *testing.T) {
	_, err := call(t, []string{"letsgo-multi", "ldflags"}, `{"project":"demo"}`)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "ldflags") {
		t.Errorf("the error should name the hook asked for, got %v", err)
	}
}

func TestRunNeedsExactlyOneArgument(t *testing.T) {
	for _, args := range [][]string{
		{"letsgo-multi"},
		{"letsgo-multi", "archive-layout", "extra"},
	} {
		if _, err := call(t, args, `{}`); err == nil {
			t.Errorf("%v should not be accepted", args)
		}
	}
}

func TestRunRejectsInputThatIsNotJSON(t *testing.T) {
	if _, err := call(t, []string{"letsgo-multi", "archive-layout"}, "not json"); err == nil {
		t.Error("expected a decode failure")
	}
}

func TestWireTypesRoundTripAsJSON(t *testing.T) {
	in := ArchiveLayoutInput{
		Project: "demo", Version: "1.0.0", Module: "example.com/demo",
		Commands: []InputCommand{{Binary: "demo", Package: "."}},
		Targets:  []string{"linux/amd64"},
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var got ArchiveLayoutInput
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Project != in.Project || len(got.Commands) != 1 {
		t.Errorf("got = %+v", got)
	}
}
