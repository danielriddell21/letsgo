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

// forge describes how the fake should answer.
type forge struct {
	push     bool
	archived bool
	repoCode int // non-zero to fail the repository lookup

	// probeCode is the status returned to the release-write probe: 403 for a
	// refusal, 422 for a token that may create releases.
	probeCode int
}

func (f forge) serve(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/releases") {
			code := f.probeCode
			if code == 0 {
				code = http.StatusInternalServerError
			}
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "probe"})
			return
		}
		if f.repoCode != 0 {
			w.WriteHeader(f.repoCode)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "Not Found"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"archived":    f.archived,
			"permissions": map[string]bool{"push": f.push},
		})
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func releasable(t *testing.T) *repo {
	t.Helper()
	r := newRepo(t)
	r.write("go.mod", "module github.com/you/foo\n\ngo 1.24\n")
	r.write("main.go", "package main\n\nfunc main() {}\n")
	r.commit("v1.0.0")
	r.git("remote", "add", "origin", "https://github.com/you/foo.git")
	return r
}

func tokenCheck(t *testing.T, r *repo, endpoint string) plan.Check {
	t.Helper()
	p, err := plan.Resolve(context.Background(), plan.Options{
		Dir: r.dir, Publish: true, Token: "test-token", APIEndpoint: endpoint,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return check(t, p, "token")
}

func TestTokenGate(t *testing.T) {
	tests := []struct {
		name    string
		actions bool
		forge   forge
		want    plan.Status
	}{
		{
			name:  "reported write access is conclusive",
			forge: forge{push: true},
			want:  plan.Pass,
		},
		{
			name:  "a user token reporting no write access is a real refusal",
			forge: forge{push: false},
			want:  plan.Fail,
		},
		{
			// The repository endpoint describes the authenticated user, and a
			// workflow token has none, so the probe supplies the answer.
			name:    "a workflow token is asked directly and permitted",
			actions: true,
			forge:   forge{push: false, probeCode: http.StatusUnprocessableEntity},
			want:    plan.Pass,
		},
		{
			name:    "a workflow token is asked directly and refused",
			actions: true,
			forge:   forge{push: false, probeCode: http.StatusForbidden},
			want:    plan.Fail,
		},
		{
			// Neither established nor refuted. Blocking on no evidence would
			// refuse correctly configured releases.
			name:    "an indeterminate probe does not block",
			actions: true,
			forge:   forge{push: false, probeCode: http.StatusInternalServerError},
			want:    plan.Warn,
		},
		{
			name:    "an unreachable repository is conclusive anywhere",
			actions: true,
			forge:   forge{repoCode: http.StatusNotFound},
			want:    plan.Fail,
		},
		{
			name:    "an archived repository is conclusive anywhere",
			actions: true,
			forge:   forge{push: true, archived: true},
			want:    plan.Fail,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.actions {
				t.Setenv("GITHUB_ACTIONS", "true")
			} else {
				t.Setenv("GITHUB_ACTIONS", "")
			}

			r := releasable(t)
			if got := tokenCheck(t, r, tt.forge.serve(t)).Status; got != tt.want {
				t.Errorf("token check = %q, want %q", got, tt.want)
			}
		})
	}
}

// A warning must not stop a release; that is the difference between "unknown"
// and "refused".
func TestUnconfirmedPermissionDoesNotBlockTheRelease(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	r := releasable(t)

	p, err := plan.Resolve(context.Background(), plan.Options{
		Dir: r.dir, Publish: true, Token: "t",
		APIEndpoint: forge{probeCode: http.StatusInternalServerError}.serve(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !p.OK() {
		t.Errorf("an unconfirmed permission blocked the release: %+v", p.Checks)
	}
}
