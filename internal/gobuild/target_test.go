package gobuild

import (
	"context"
	"strings"
	"testing"
)

func TestParseTarget(t *testing.T) {
	good := map[string]Target{
		"linux/amd64":   {OS: "linux", Arch: "amd64"},
		"darwin/arm64":  {OS: "darwin", Arch: "arm64"},
		" linux/arm64 ": {OS: "linux", Arch: "arm64"},
		"linux / amd64": {OS: "linux", Arch: "amd64"},
		"js/wasm":       {OS: "js", Arch: "wasm"},
	}
	for in, want := range good {
		t.Run(in, func(t *testing.T) {
			got, err := ParseTarget(in)
			if err != nil {
				t.Fatalf("ParseTarget(%q): %v", in, err)
			}
			if got != want {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
	}

	bad := []string{"", "linux", "/amd64", "linux/", "linux/amd64/v3", "   "}
	for _, in := range bad {
		t.Run("bad:"+in, func(t *testing.T) {
			if _, err := ParseTarget(in); err == nil {
				t.Errorf("ParseTarget(%q) succeeded, want an error", in)
			}
		})
	}
}

func TestParseTargetsRejectsDuplicates(t *testing.T) {
	if _, err := ParseTargets([]string{"linux/amd64", "darwin/arm64", "linux/amd64"}); err == nil {
		t.Error("ParseTargets accepted a duplicate, want an error")
	}
}

func TestTargetExt(t *testing.T) {
	if got := (Target{OS: "windows", Arch: "amd64"}).Ext(); got != ".exe" {
		t.Errorf("windows ext = %q, want .exe", got)
	}
	if got := (Target{OS: "linux", Arch: "amd64"}).Ext(); got != "" {
		t.Errorf("linux ext = %q, want empty", got)
	}
}

func TestValidate(t *testing.T) {
	ctx := context.Background()

	if err := Validate(ctx, "", ReleaseTargets); err != nil {
		t.Errorf("the default matrix should be buildable: %v", err)
	}

	if err := Validate(ctx, "", nil); err == nil {
		t.Error("Validate accepted an empty matrix, want an error")
	}

	err := Validate(ctx, "", []Target{{OS: "linux", Arch: "amd64"}, {OS: "linux", Arch: "aarch64"}})
	if err == nil {
		t.Fatal("Validate accepted linux/aarch64, want an error")
	}
	if !strings.Contains(err.Error(), "aarch64") {
		t.Errorf("error does not name the bad target:\n%s", err)
	}
	// aarch64 is what the rest of the world calls arm64, so this is a mistake
	// worth guiding rather than merely rejecting.
	if !strings.Contains(err.Error(), "did you mean") {
		t.Errorf("error offers no suggestion:\n%s", err)
	}
}

func TestSupportedIncludesHost(t *testing.T) {
	supported, err := Supported(context.Background(), "")
	if err != nil {
		t.Fatalf("Supported: %v", err)
	}
	if len(supported) < 20 {
		t.Errorf("only %d targets reported, expected many more", len(supported))
	}

	host := Host()
	for _, target := range supported {
		if target == host {
			return
		}
	}
	t.Errorf("supported targets do not include the host %s", host)
}
