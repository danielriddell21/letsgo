package plan_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/plan"
)

// tapForge answers the repository endpoint for both repositories, and reports
// write access to the tap only to a request carrying tapToken.
//
// That asymmetry is the arrangement the split exists for: a workflow token
// writes the release and cannot reach the tap, and an App token reaches the
// tap and is installed nowhere else. A gate that probed the release token
// against the tap would pass here and the release would then fail on its last
// step, which is the failure this is guarding.
func tapForge(t *testing.T, tapToken string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "probe"})
			return
		}
		// Keyed on the release repository rather than the tap's, because
		// `brew you/tap` resolves to you/homebrew-tap: anything that is not
		// the release repository is the tap.
		push := strings.HasSuffix(r.URL.Path, "/you/foo")
		if !push {
			push = r.Header.Get("Authorization") == "Bearer "+tapToken
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"archived":    false,
			"permissions": map[string]bool{"push": push},
		})
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// noAmbientTokens clears everything the resolver reads, so that a machine with
// a real token in its environment runs the same test as one without.
func noAmbientTokens(t *testing.T) {
	t.Helper()
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("LETSGO_TAP_TOKEN", "")
}

// withTap is a releasable repository that publishes a formula.
func withTap(t *testing.T) *repo {
	t.Helper()
	r := releasable(t)
	r.write(plan.ConfigFile, "brew you/tap\n")
	return r
}

func tapCheck(t *testing.T, r *repo, opts plan.Options) plan.Check {
	t.Helper()
	opts.Dir, opts.Publish = r.dir, true
	p, err := plan.Resolve(context.Background(), opts)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return check(t, p, "brew tap")
}

func TestTapGateProbesTheTapToken(t *testing.T) {
	noAmbientTokens(t)

	tests := []struct {
		name     string
		token    string
		tapToken string
		env      string
		want     plan.Status
	}{
		{
			// The single-credential arrangement, unchanged: one token that
			// can write to both.
			name:  "one token that reaches the tap still passes",
			token: "tap-token",
			want:  plan.Pass,
		},
		{
			// The failure #21 describes. Without a tap token the release
			// token is probed, and it cannot write to the tap.
			name:  "a release token that cannot reach the tap fails",
			token: "release-token",
			want:  plan.Fail,
		},
		{
			name:     "--tap-token is probed rather than --token",
			token:    "release-token",
			tapToken: "tap-token",
			want:     plan.Pass,
		},
		{
			name:  "LETSGO_TAP_TOKEN is probed rather than --token",
			token: "release-token",
			env:   "tap-token",
			want:  plan.Pass,
		},
		{
			// Precedence: the flag wins, so a stale environment variable on a
			// developer's machine cannot quietly decide a release.
			name:     "--tap-token wins over the environment",
			token:    "release-token",
			tapToken: "tap-token",
			env:      "wrong-token",
			want:     plan.Pass,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LETSGO_TAP_TOKEN", tt.env)
			r := withTap(t)
			got := tapCheck(t, r, plan.Options{
				Token:       tt.token,
				TapToken:    tt.tapToken,
				APIEndpoint: tapForge(t, "tap-token"),
			}).Status
			if got != tt.want {
				t.Errorf("brew tap check = %q, want %q", got, tt.want)
			}
		})
	}
}

// The release token is still what the release itself is checked against, so
// separating the two must not let a tap token stand in for it.
func TestTapTokenDoesNotStandInForTheReleaseToken(t *testing.T) {
	noAmbientTokens(t)

	r := withTap(t)
	p, err := plan.Resolve(context.Background(), plan.Options{
		Dir: r.dir, Publish: true, TapToken: "tap-token",
		APIEndpoint: tapForge(t, "tap-token"),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := check(t, p, "token").Status; got != plan.Fail {
		t.Errorf("token check = %q, want %q: no release token was given", got, plan.Fail)
	}
}

func TestTapTokenResolution(t *testing.T) {
	noAmbientTokens(t)
	t.Setenv("GITHUB_TOKEN", "release-env")

	tests := []struct {
		name       string
		override   string
		token      string
		env        string
		wantToken  string
		wantSource string
	}{
		{
			name:       "the flag wins",
			override:   "flag",
			env:        "env",
			wantToken:  "flag",
			wantSource: "--tap-token",
		},
		{
			name:       "then the environment",
			env:        "env",
			wantToken:  "env",
			wantSource: "LETSGO_TAP_TOKEN",
		},
		{
			name:       "then the release token's flag",
			token:      "release-flag",
			wantToken:  "release-flag",
			wantSource: "--token",
		},
		{
			// The fallback that keeps every existing repository working.
			name:       "and finally the release token's environment",
			wantToken:  "release-env",
			wantSource: "GITHUB_TOKEN",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LETSGO_TAP_TOKEN", tt.env)
			token, source := plan.TapToken(tt.override, tt.token)
			if token != tt.wantToken || source != tt.wantSource {
				t.Errorf("TapToken(%q, %q) = %q from %q, want %q from %q",
					tt.override, tt.token, token, source, tt.wantToken, tt.wantSource)
			}
		})
	}
}
