package discover

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNestedModuleDirs(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/foo\n")
	write(t, dir, "services/api/go.mod", "module example.com/foo/services/api\n")
	write(t, dir, "services/api/internal/tool/go.mod", "module example.com/foo/services/api/tool\n")
	write(t, dir, "web/go.mod", "module example.com/foo/web\n")
	write(t, dir, "web/vendor/other/go.mod", "module example.com/vendored\n")

	got, err := NestedModuleDirs(dir)
	if err != nil {
		t.Fatalf("NestedModuleDirs: %v", err)
	}
	want := []string{"services/api", "services/api/internal/tool", "web"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNestedModuleDirsRelativeToANestedModuleItself(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/foo\n")
	write(t, dir, "services/api/go.mod", "module example.com/foo/services/api\n")
	write(t, dir, "services/api/internal/tool/go.mod", "module example.com/foo/services/api/tool\n")

	got, err := NestedModuleDirs(filepath.Join(dir, "services", "api"))
	if err != nil {
		t.Fatalf("NestedModuleDirs: %v", err)
	}
	if strings.Join(got, ",") != "internal/tool" {
		t.Errorf("got %v, want [internal/tool]", got)
	}
}
