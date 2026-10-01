package gobuild

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// module writes a one-package module whose main is body, and returns its dir.
func module(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":  "module example.com/tiny\n\ngo 1.21\n",
		"main.go": body,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestBuild(t *testing.T) {
	goBin, _, err := Toolchain(nil)
	if err != nil {
		t.Skipf("no go command: %v", err)
	}

	tests := []struct {
		name    string
		source  string
		tags    []string
		wantErr string
	}{
		{name: "compiles", source: "package main\n\nfunc main() {}\n"},
		{name: "with tags", source: "package main\n\nfunc main() {}\n", tags: []string{"netgo"}},
		{name: "reports the compiler's words", source: "package main\n\nfunc main() { undefined() }\n", wantErr: "undefined"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := Host()
			out := filepath.Join(t.TempDir(), "tiny"+host.Ext())

			err := Build(context.Background(), Request{
				Dir:     module(t, tt.source),
				Package: ".",
				Output:  out,
				Target:  host,
				GoBin:   goBin,
				Tags:    tt.tags,
			})

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(out); err != nil {
				t.Errorf("no binary written: %v", err)
			}
		})
	}
}

func TestBuildRequiresItsInputs(t *testing.T) {
	full := Request{Dir: "d", Package: ".", Output: "o", GoBin: "go"}
	tests := []struct {
		name   string
		mutate func(*Request)
		want   string
	}{
		{"dir", func(r *Request) { r.Dir = "" }, "Dir"},
		{"package", func(r *Request) { r.Package = "" }, "Package"},
		{"output", func(r *Request) { r.Output = "" }, "Output"},
		{"go command", func(r *Request) { r.GoBin = "" }, "GoBin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := full
			tt.mutate(&req)
			err := Build(context.Background(), req)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to name %s", err, tt.want)
			}
		})
	}
}

func TestVersion(t *testing.T) {
	goBin, _, err := Toolchain(nil)
	if err != nil {
		t.Skipf("no go command: %v", err)
	}

	t.Run("reads the version", func(t *testing.T) {
		got, err := Version(context.Background(), goBin)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(got, "go") {
			t.Errorf("version = %q, want a go version", got)
		}
	})

	t.Run("requires the go command", func(t *testing.T) {
		if _, err := Version(context.Background(), ""); err == nil {
			t.Error("an empty go command should be an error")
		}
	})

	t.Run("reports a command that cannot run", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "go")
		if _, err := Version(context.Background(), missing); err == nil {
			t.Error("a missing go command should be an error")
		}
	})
}
