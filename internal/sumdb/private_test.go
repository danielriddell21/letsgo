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
		{
			name:       "GOPRIVATE match",
			env:        map[string]string{"GOPRIVATE": "example.com/*"},
			modulePath: "example.com/x",
			wantSkip:   true,
			wantReason: "GOPRIVATE",
		},
		{
			name:       "no match",
			env:        map[string]string{"GOPRIVATE": "example.com/*"},
			modulePath: "other.com/x",
			wantSkip:   false,
		},
		{
			name:       "no env set",
			modulePath: "example.com/x",
			wantSkip:   false,
		},
		{
			name:       "GONOSUMDB match",
			env:        map[string]string{"GONOSUMDB": "example.com/*"},
			modulePath: "example.com/x",
			wantSkip:   true,
			wantReason: "GONOSUMDB",
		},
		{
			name:       "GONOSUMCHECK match",
			env:        map[string]string{"GONOSUMCHECK": "example.com/*"},
			modulePath: "example.com/x",
			wantSkip:   true,
			wantReason: "GONOSUMCHECK",
		},
		{
			name:       "multiple comma-separated globs",
			env:        map[string]string{"GOPRIVATE": "foo.com/*,example.com/*,bar.com/*"},
			modulePath: "example.com/x",
			wantSkip:   true,
			wantReason: "GOPRIVATE",
		},
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
