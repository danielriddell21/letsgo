package plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
	// Closed explicitly, not deferred: run reads from os.Stdin before this
	// function returns, and an open handle left past that point is what
	// stops Windows from letting t.TempDir() remove the file afterwards.
	defer func() { _ = input.Close() }()
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

// buildHelper compiles testdata/helper, a real func main built on Main, into
// a binary the tests below can run as a subprocess. Main's job is checking
// os.Args and calling os.Exit, neither of which is exercisable in-process
// without ending the test binary itself — so this is the one place Main
// runs for real, from the outside.
func buildHelper(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "helper")
	cmd := exec.Command("go", "build", "-o", bin, "./testdata/helper")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build testdata/helper: %v\n%s", err, out)
	}
	return bin
}

// Main is the whole of a plugin's func main, and it answers correctly when
// letsgo invokes it with the hook it implements.
func TestMainAnswersItsHook(t *testing.T) {
	bin := buildHelper(t)

	cmd := exec.Command(bin, string(HookArchiveLayout))
	cmd.Stdin = strings.NewReader(`{"project":"demo"}`)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	var got ArchiveLayoutOutput
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("stdout is not one JSON object: %q", out)
	}
	if len(got.Archives) != 1 || got.Archives[0].Name != "demo" {
		t.Errorf("got = %+v", got)
	}
}

// A plugin invoked for a hook it does not answer exits non-zero and says so,
// rather than answering the wrong question silently.
func TestMainExitsNonZeroForTheWrongHook(t *testing.T) {
	bin := buildHelper(t)

	cmd := exec.Command(bin, string(HookLDFlags))
	cmd.Stdin = strings.NewReader(`{}`)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() == 0 {
		t.Fatalf("expected a non-zero exit, got %v", err)
	}
	if !strings.Contains(stderr.String(), string(HookLDFlags)) {
		t.Errorf("stderr = %q, want it to name the hook asked for", stderr.String())
	}
}

// withStdio runs fn with os.Args, os.Stdin and os.Stdout swapped for the
// given values, restoring them afterwards.
func withStdio(t *testing.T, args []string, stdin string, fn func(stdout *os.File)) {
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
	defer func() { _ = input.Close() }()
	if _, err := input.WriteString(stdin); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := input.Seek(0, 0); err != nil {
		t.Fatalf("seek: %v", err)
	}

	oldArgs, oldIn, oldOut := os.Args, os.Stdin, os.Stdout
	os.Args, os.Stdin, os.Stdout = args, input, w
	fn(w)
	os.Args, os.Stdin, os.Stdout = oldArgs, oldIn, oldOut
	_ = w.Close()
}

// The answer function's own error reaches the caller unchanged: run adds
// nothing to it, because there is nothing wrong with the hook or the wire
// format, only with what the plugin decided.
func TestRunPropagatesTheAnswerFunctionsError(t *testing.T) {
	var runErr error
	withStdio(t, []string{"letsgo-multi", "archive-layout"}, `{}`, func(*os.File) {
		runErr = run(HookArchiveLayout, func(testIn) (testOut, error) {
			return testOut{}, errors.New("boom")
		})
	})
	if runErr == nil || runErr.Error() != "boom" {
		t.Errorf("err = %v, want boom", runErr)
	}
}

type unencodable struct {
	Ch chan int `json:"ch"`
}

// An answer that cannot be encoded is reported like any other failure, not
// left to panic or write a truncated stdout.
func TestRunReportsAnEncodingFailure(t *testing.T) {
	var runErr error
	withStdio(t, []string{"letsgo-multi", "archive-layout"}, `{}`, func(*os.File) {
		runErr = run(HookArchiveLayout, func(testIn) (unencodable, error) {
			return unencodable{Ch: make(chan int)}, nil
		})
	})
	if runErr == nil || !strings.Contains(runErr.Error(), "writing the hook's answer") {
		t.Errorf("err = %v, want an encoding failure", runErr)
	}
}
