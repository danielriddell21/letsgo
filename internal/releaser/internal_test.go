package releaser

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/internal/plan"
)

// A release only reads the repository's description when there is a Homebrew
// tap to write into: a formula's, or a tap-files plugin's cask.
func TestWantsRepoInfo(t *testing.T) {
	tap := github.Repo{Owner: "you", Name: "homebrew-tap"}

	for _, tc := range []struct {
		name string
		p    *plan.Plan
		want bool
	}{
		{"no tap", &plan.Plan{HasRepo: true}, false},
		{"tap but no repository", &plan.Plan{Tap: tap}, false},
		{"tap and repository", &plan.Plan{Tap: tap, HasRepo: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := WantsRepoInfo(tc.p); got != tc.want {
				t.Errorf("WantsRepoInfo(%+v) = %v, want %v", tc.p, got, tc.want)
			}
		})
	}
}

// Not being able to read the description is not a reason to stop a release.
func TestDescribeRepoReportsAFailureAndReturnsNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	client := github.New("t")
	client.SetEndpoints(server.URL, server.URL)

	var log bytes.Buffer
	info := DescribeRepo(context.Background(), client, github.Repo{Owner: "you", Name: "demo"}, &log)
	if info != nil {
		t.Errorf("info = %+v, want nil", info)
	}
	if !strings.Contains(log.String(), "could not read you/demo's description") {
		t.Errorf("failure not reported: %q", log.String())
	}

	// A nil log discards the report rather than panicking.
	if DescribeRepo(context.Background(), client, github.Repo{Owner: "you", Name: "demo"}, nil) != nil {
		t.Error("info should still be nil with no log")
	}
}

func TestFileSum(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := fileSum(path)
	want := sha256.Sum256([]byte("x"))
	if err != nil || !bytes.Equal(got, want[:]) {
		t.Errorf("fileSum = %x, %v, want %x", got, err, want)
	}
	if _, err := fileSum(path + ".missing"); err == nil {
		t.Error("fileSum of a missing file succeeded")
	}
}
