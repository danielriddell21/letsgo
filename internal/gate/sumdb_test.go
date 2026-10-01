package gate

import (
	"strings"
	"testing"
)

func TestDecideSumdb(t *testing.T) {
	tests := []struct {
		name         string
		in           SumdbInput
		wantRun      bool
		wantByConfig bool
		wantReason   string
	}{
		{name: "a public tagged release runs", in: SumdbInput{ModulePath: "github.com/o/r"}, wantRun: true},
		{name: "disable sumdb", in: SumdbInput{Disabled: true, ModulePath: "github.com/o/r"}, wantByConfig: true, wantReason: "disabled by config"},
		{name: "snapshot", in: SumdbInput{Snapshot: true, ModulePath: "github.com/o/r"}, wantReason: "not a tagged release"},
		{name: "untagged", in: SumdbInput{Untagged: true, ModulePath: "github.com/o/r"}, wantReason: "not a tagged release"},
		{name: "draft", in: SumdbInput{Draft: true, ModulePath: "github.com/o/r"}, wantReason: "a draft release is not public yet"},
		{name: "scoped module", in: SumdbInput{Scoped: true, ModulePath: "github.com/o/r"}, wantReason: "the module is not at the repository root"},
		{name: "proxy warm off", in: SumdbInput{ProxyWarmOff: true, ModulePath: "github.com/o/r"}, wantReason: "proxy-warm is disabled, so sum.golang.org has no record to compare"},
		{name: "private module", in: SumdbInput{ModulePath: "internal.example"}, wantReason: "private module"},
	}
	t.Setenv("GOPRIVATE", "internal.example")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DecideSumdb(tt.in)
			if got.Run != tt.wantRun || got.ByConfig != tt.wantByConfig {
				t.Fatalf("DecideSumdb() = %+v, want run=%v byConfig=%v", got, tt.wantRun, tt.wantByConfig)
			}
			if tt.wantReason != "" && !strings.Contains(got.Reason, tt.wantReason) {
				t.Errorf("Reason = %q, want it to contain %q", got.Reason, tt.wantReason)
			}
		})
	}
}
