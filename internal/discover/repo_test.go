package discover

import (
	"strings"
	"testing"
)

func TestParseRemote(t *testing.T) {
	want := Repo{Host: "github.com", Owner: "danielriddell21", Name: "letsgo"}

	urls := []string{
		"https://github.com/danielriddell21/letsgo",
		"https://github.com/danielriddell21/letsgo.git",
		"https://github.com/danielriddell21/letsgo/",
		"http://github.com/danielriddell21/letsgo.git",
		"git@github.com:danielriddell21/letsgo.git",
		"git@github.com:danielriddell21/letsgo",
		"ssh://git@github.com/danielriddell21/letsgo.git",
		"https://token@github.com/danielriddell21/letsgo.git",
		"https://user:pass@github.com/danielriddell21/letsgo.git",
		"ssh://git@github.com:22/danielriddell21/letsgo.git",
		"  https://github.com/danielriddell21/letsgo.git  ",
	}

	for _, url := range urls {
		t.Run(url, func(t *testing.T) {
			got, err := ParseRemote(url)
			if err != nil {
				t.Fatalf("ParseRemote: %v", err)
			}
			if got != want {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
	}
}

// A remote carrying a token must not leak it into anything we derive from it.
func TestParseRemoteDropsCredentials(t *testing.T) {
	got, err := ParseRemote("https://ghp_secrettoken@github.com/you/foo.git")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.String(), "ghp_") {
		t.Errorf("credential survived parsing: %s", got)
	}
}

func TestParseRemoteRejectsGarbage(t *testing.T) {
	for _, url := range []string{"", "not-a-url", "https://github.com/only-owner"} {
		if _, err := ParseRemote(url); err == nil {
			t.Errorf("ParseRemote(%q) succeeded, want an error", url)
		}
	}
}
