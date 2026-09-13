package release

import (
	"fmt"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/bytesize"
)

func artifact(target string, binarySize bytesize.Size) build.Artifact {
	os, arch, _ := strings.Cut(target, "/")
	return build.Artifact{Target: target, OS: os, Arch: arch, BinarySize: int64(binarySize)}
}

func TestCheckBudgetsPasses(t *testing.T) {
	artifacts := []build.Artifact{artifact("linux/amd64", 10*bytesize.MB)}
	budgets := map[string]bytesize.Size{"linux/amd64": 15 * bytesize.MB}

	var warnings []string
	err := checkBudgets(artifacts, budgets, func(format string, args ...any) {
		warnings = append(warnings, format)
	})

	if err != nil {
		t.Fatalf("checkBudgets: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
}

func TestCheckBudgetsReportsEveryOverrun(t *testing.T) {
	artifacts := []build.Artifact{
		artifact("linux/amd64", 16*bytesize.MB),
		artifact("darwin/arm64", 20*bytesize.MB),
		artifact("windows/amd64", 1*bytesize.MB),
	}
	budgets := map[string]bytesize.Size{
		"linux/amd64":   15 * bytesize.MB,
		"darwin/arm64":  15 * bytesize.MB,
		"windows/amd64": 15 * bytesize.MB,
	}

	err := checkBudgets(artifacts, budgets, nil)

	if err == nil {
		t.Fatal("want an error")
	}
	// Reporting only the first overrun would mean one rebuild per oversized
	// target.
	for _, want := range []string{"linux/amd64", "darwin/arm64", "over its 15.0 MB budget"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error is missing %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "windows/amd64") {
		t.Errorf("an artifact under budget was reported: %v", err)
	}
}

func TestCheckBudgetsWarnsWhenClose(t *testing.T) {
	artifacts := []build.Artifact{artifact("linux/amd64", 14*bytesize.MB)}
	budgets := map[string]bytesize.Size{"linux/amd64": 15 * bytesize.MB}

	var warnings []string
	err := checkBudgets(artifacts, budgets, func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	})

	if err != nil {
		t.Fatalf("checkBudgets: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "93% of its 15.0 MB budget") {
		t.Fatalf("warnings = %v", warnings)
	}
}

// An artifact whose target has no budget is not a problem; a project that
// caps one target and not another is an ordinary thing to want.
func TestCheckBudgetsIgnoresUncappedTargets(t *testing.T) {
	artifacts := []build.Artifact{artifact("linux/amd64", 100*bytesize.MB)}

	if err := checkBudgets(artifacts, map[string]bytesize.Size{"darwin/arm64": bytesize.MB}, nil); err != nil {
		t.Fatalf("checkBudgets: %v", err)
	}
	if err := checkBudgets(artifacts, nil, nil); err != nil {
		t.Fatalf("checkBudgets with no budgets: %v", err)
	}
}
