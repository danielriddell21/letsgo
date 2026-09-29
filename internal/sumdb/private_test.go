package sumdb

import "testing"

func TestPrivateModule(t *testing.T) {
	tests := []struct {
		name       string
		env        map[string]string
		modulePath string
		wantSkip   bool
		wantReason string
	}{
		{"GOPRIVATE match", map[string]string{"GOPRIVATE": "example.com/*"}, "example.com/x", true, "GOPRIVATE"},
		{"no match", map[string]string{"GOPRIVATE": "example.com/*"}, "other.com/x", false, ""},
		{"no env set", nil, "example.com/x", false, ""},
		{"GONOSUMDB match", map[string]string{"GONOSUMDB": "example.com/*"}, "example.com/x", true, "GONOSUMDB"},
		{"GONOSUMCHECK match", map[string]string{"GONOSUMCHECK": "example.com/*"}, "example.com/x", true, "GONOSUMCHECK"},
		{"multiple comma-separated globs", map[string]string{"GOPRIVATE": "foo.com/*,example.com/*,bar.com/*"}, "example.com/x", true, "GOPRIVATE"},
		{"empty glob segment between commas is skipped", map[string]string{"GOPRIVATE": ",example.com/*,"}, "example.com/x", true, "GOPRIVATE"},
		{"target with more path elements than the glob is truncated to match", map[string]string{"GOPRIVATE": "example.com/*"}, "example.com/foo/bar/baz", true, "GOPRIVATE"},
		{"glob with more path elements than the target never matches", map[string]string{"GOPRIVATE": "example.com/foo/*"}, "example.com/x", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, name := range []string{"GOPRIVATE", "GONOSUMDB", "GONOSUMCHECK"} {
				t.Setenv(name, tt.env[name])
			}

			skip, reason := PrivateModule(tt.modulePath)
			if skip != tt.wantSkip {
				t.Errorf("PrivateModule(%q) skip = %v, want %v", tt.modulePath, skip, tt.wantSkip)
			}
			if reason != tt.wantReason {
				t.Errorf("PrivateModule(%q) reason = %q, want %q", tt.modulePath, reason, tt.wantReason)
			}
		})
	}
}
