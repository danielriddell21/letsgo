package discover

import (
	"testing"
)

func TestInspectVars(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "main.go", `package main

const buildID = "const-value"

var (
	version   string
	commit    = "none"
	buildDate = currentDate()
	retries   int
	Version   = "capitalised"
)

func currentDate() string { return "" }

func main() {}
`)
	// A test file must not contribute symbols: it is not linked into the
	// released binary.
	write(t, dir, "main_test.go", `package main

var onlyInTests = "should not be found"
`)

	// Deliberately misspelled: this is the input the suggestion logic exists
	// to recognise, so it must not be "corrected".
	const typo = "verison" //nolint:misspell // the typo under test

	names := []string{"version", "commit", "buildDate", "retries", "buildID", "missing", typo, "onlyInTests"}
	got, err := InspectVars(dir, names)
	if err != nil {
		t.Fatalf("InspectVars: %v", err)
	}

	byName := map[string]Symbol{}
	for _, s := range got {
		byName[s.Name] = s
	}

	want := map[string]SymbolStatus{
		"version":     SymbolOK,
		"commit":      SymbolOK,
		"buildDate":   SymbolDynamicInit,
		"retries":     SymbolNotString,
		"buildID":     SymbolIsConst,
		"missing":     SymbolMissing,
		typo:          SymbolMissing,
		"onlyInTests": SymbolMissing,
	}

	for name, wantStatus := range want {
		if got := byName[name].Status; got != wantStatus {
			t.Errorf("%s: status = %q, want %q (detail: %s)", name, got, wantStatus, byName[name].Detail)
		}
	}

	// The overwhelmingly common form of this mistake is a case difference.
	if s := byName[typo]; s.Suggestion == "" {
		t.Errorf("%s: expected a suggestion", typo)
	}
	if s := byName["version"]; !s.OK() {
		t.Error("version should be usable")
	}
}

func TestFindMainPackages(t *testing.T) {
	t.Run("cmd directory wins", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "go.mod", "module example.com/foo\n")
		write(t, dir, "foo.go", "package foo\n")
		write(t, dir, "cmd/alpha/main.go", "package main\nfunc main() {}\n")
		write(t, dir, "cmd/beta/main.go", "package main\nfunc main() {}\n")
		write(t, dir, "cmd/notacommand/lib.go", "package notacommand\n")

		got, err := FindMainPackages(dir, "foo")
		if err != nil {
			t.Fatalf("FindMainPackages: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("found %d packages, want 2: %+v", len(got), got)
		}
		// Sorted, so the order is stable across filesystems.
		if got[0].RelPath != "./cmd/alpha" || got[1].RelPath != "./cmd/beta" {
			t.Errorf("got %q and %q", got[0].RelPath, got[1].RelPath)
		}
		if got[0].BinaryName != "alpha" {
			t.Errorf("BinaryName = %q, want alpha", got[0].BinaryName)
		}
	})

	t.Run("module root", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "go.mod", "module example.com/foo\n")
		write(t, dir, "main.go", "package main\nfunc main() {}\n")

		got, err := FindMainPackages(dir, "foo")
		if err != nil {
			t.Fatalf("FindMainPackages: %v", err)
		}
		if len(got) != 1 || got[0].RelPath != "." || got[0].BinaryName != "foo" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("library has no commands", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, "go.mod", "module example.com/foo\n")
		write(t, dir, "foo.go", "package foo\n")

		if _, err := FindMainPackages(dir, "foo"); err == nil {
			t.Error("FindMainPackages succeeded for a library, want an error")
		}
	})
}
