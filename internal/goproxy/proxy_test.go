package goproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveProxyDefaultsWithNothingSet(t *testing.T) {
	proxy, source := ResolveProxy("", "", "")
	if proxy != DefaultProxy || source != "the default" {
		t.Errorf("got %q from %q, want %q from the default", proxy, source, DefaultProxy)
	}
}

func TestResolveProxyHonoursTheGlobalConfig(t *testing.T) {
	proxy, source := ResolveProxy("", "https://p.internal", "/etc/letsgo/config.mod")
	if proxy != "https://p.internal" || source != "/etc/letsgo/config.mod" {
		t.Errorf("got %q from %q, want the global proxy", proxy, source)
	}
}

func TestResolveProxyGOPROXYOutranksTheGlobalConfig(t *testing.T) {
	proxy, source := ResolveProxy("https://env.example", "https://p.internal", "config.mod")
	if proxy != "https://env.example" || source != "GOPROXY" {
		t.Errorf("got %q from %q, want the env proxy", proxy, source)
	}
}

func TestEscapeModulePath(t *testing.T) {
	cases := map[string]string{
		"github.com/you/foo":            "github.com/you/foo",
		"github.com/BurntSushi/toml":    "github.com/!burnt!sushi/toml",
		"github.com/danielriddell21/lg": "github.com/danielriddell21/lg",
		"example.com/Foo/Bar/v2":        "example.com/!foo/!bar/v2",
		"v1.2.3":                        "v1.2.3",
		"v1.2.3-RC1":                    "v1.2.3-!r!c1",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := EscapeModulePath(in); got != want {
				t.Errorf("EscapeModulePath(%q) = %q, want %q", in, got, want)
			}
		})
	}
}

func TestWarmProxyRequestsTheRightPath(t *testing.T) {
	var requested string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.URL.Path
		w.Write([]byte(`{"Version":"v1.2.3"}`))
	}))
	defer server.Close()

	if err := WarmProxy(context.Background(), server.URL, "github.com/BurntSushi/toml", "1.2.3"); err != nil {
		t.Fatalf("WarmProxy: %v", err)
	}

	want := "/github.com/!burnt!sushi/toml/@v/v1.2.3.info"
	if requested != want {
		t.Errorf("requested %q, want %q", requested, want)
	}
}

// A version already carrying its v must not gain a second one.
func TestWarmProxyNormalisesVersionPrefix(t *testing.T) {
	var requested string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.URL.Path
	}))
	defer server.Close()

	for _, version := range []string{"1.2.3", "v1.2.3"} {
		if err := WarmProxy(context.Background(), server.URL, "example.com/foo", version); err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(requested, "/@v/v1.2.3.info") {
			t.Errorf("version %q produced path %q", version, requested)
		}
	}
}

// Warming is best effort: an unreachable proxy has not broken a release that
// is already published.
func TestWarmProxyReportsFailureWithoutPanicking(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	if err := WarmProxy(context.Background(), server.URL, "example.com/foo", "v1.0.0"); err == nil {
		t.Error("WarmProxy reported success for a 404")
	}
	if err := WarmProxy(context.Background(), server.URL, "", "v1.0.0"); err == nil {
		t.Error("WarmProxy accepted an empty module path")
	}
}
