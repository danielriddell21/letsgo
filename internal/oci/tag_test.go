package oci_test

import (
	"testing"

	"github.com/danielriddell21/letsgo/internal/oci"
)

func TestChannel(t *testing.T) {
	tests := []struct {
		version string
		want    string
		wantOK  bool
	}{
		{"1.3.0-rc.1", "rc", true},
		{"1.3.0-beta.2.foo", "beta", true},
		{"1.3.0", "", false},
		{"not-a-version", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			got, ok := oci.Channel(tt.version)
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("Channel(%q) = %q, %v, want %q, %v", tt.version, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
