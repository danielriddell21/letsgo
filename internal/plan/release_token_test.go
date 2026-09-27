package plan_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielriddell21/letsgo/internal/plan"
)

// releaseForge answers the release repository's endpoint, reporting push
// access only to a request carrying releaseToken.
//
// That asymmetry is the arrangement the split exists for: a workflow token
// can read the repository but was never meant to publish under a bot
// identity, and a token scoped to that identity can. A gate that probed the
// plain token for this would pass here and the release would then be
// created under the wrong account, which is the mistake #44 exists to fix.
func releaseForge(t *testing.T, releaseToken string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "probe"})
			return
		}
		push := r.Header.Get("Authorization") == "Bearer "+releaseToken
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"archived": false, "permissions": map[string]bool{"push": push},
		})
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func releaseTokenCheck(t *testing.T, opts plan.Options) plan.Check {
	t.Helper()
	r := releasable(t)
	opts.Dir, opts.Publish = r.dir, true
	p, err := plan.Resolve(context.Background(), opts)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return check(t, p, "token")
}

func noAmbientReleaseTokens(t *testing.T) {
	t.Helper()
	noAmbientTokens(t)
	t.Setenv("LETSGO_RELEASE_TOKEN", "")
}

func TestReleaseGateProbesTheReleaseToken(t *testing.T) {
	tests := []struct {
		name         string
		token        string
		releaseToken string
		env          string
		serverAllows string
		want         plan.Status
	}{
		{
			// The single-credential arrangement, unchanged: one token that
			// creates the release itself.
			name:         "one token that can create the release still passes",
			token:        "workflow-token",
			serverAllows: "workflow-token",
			want:         plan.Pass,
		},
		{
			name:         "a workflow token that cannot create the release fails",
			token:        "workflow-token",
			serverAllows: "release-token",
			want:         plan.Fail,
		},
		{
			name:         "--release-token is probed rather than --token",
			token:        "workflow-token",
			releaseToken: "release-token",
			serverAllows: "release-token",
			want:         plan.Pass,
		},
		{
			name:         "LETSGO_RELEASE_TOKEN is probed rather than --token",
			token:        "workflow-token",
			env:          "release-token",
			serverAllows: "release-token",
			want:         plan.Pass,
		},
		{
			// Precedence: the flag wins, so a stale environment variable on a
			// developer's machine cannot quietly decide who publishes.
			name:         "--release-token wins over the environment",
			token:        "workflow-token",
			releaseToken: "release-token",
			env:          "wrong-token",
			serverAllows: "release-token",
			want:         plan.Pass,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			noAmbientReleaseTokens(t)
			t.Setenv("LETSGO_RELEASE_TOKEN", tt.env)

			got := releaseTokenCheck(t, plan.Options{
				Token: tt.token, ReleaseToken: tt.releaseToken, APIEndpoint: releaseForge(t, tt.serverAllows),
			}).Status
			if got != tt.want {
				t.Errorf("token check = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReleaseTokenResolution(t *testing.T) {
	noAmbientReleaseTokens(t)
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
			wantSource: "--release-token",
		},
		{
			name:       "then the environment",
			env:        "env",
			wantToken:  "env",
			wantSource: "LETSGO_RELEASE_TOKEN",
		},
		{
			name:       "then the plain token's flag",
			token:      "token-flag",
			wantToken:  "token-flag",
			wantSource: "--token",
		},
		{
			// The fallback that keeps every existing repository working.
			name:       "and finally the plain token's environment",
			wantToken:  "release-env",
			wantSource: "GITHUB_TOKEN",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LETSGO_RELEASE_TOKEN", tt.env)
			token, source := plan.ReleaseToken(tt.override, tt.token)
			if token != tt.wantToken || source != tt.wantSource {
				t.Errorf("ReleaseToken(%q, %q) = %q from %q, want %q from %q",
					tt.override, tt.token, token, source, tt.wantToken, tt.wantSource)
			}
		})
	}
}
