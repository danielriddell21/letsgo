package discover

import (
	"context"
	"fmt"
	"strings"
)

// Repo identifies the forge repository a release is published to.
type Repo struct {
	// Host is the forge hostname, e.g. "github.com".
	Host string

	// Owner is the user or organisation.
	Owner string

	// Name is the repository name, without any ".git" suffix.
	Name string
}

func (r Repo) String() string { return r.Host + "/" + r.Owner + "/" + r.Name }

// FindRepo derives the repository from the "origin" remote.
func FindRepo(ctx context.Context, dir string) (Repo, error) {
	url, err := git(ctx, dir, "remote", "get-url", "origin")
	if err != nil {
		return Repo{}, fmt.Errorf("discover: no 'origin' remote: %w", err)
	}
	return ParseRemote(url)
}

// ParseRemote understands the URL forms git itself accepts.
func ParseRemote(url string) (Repo, error) {
	original := url
	url = strings.TrimSpace(url)

	switch {
	case strings.HasPrefix(url, "git@"):
		// scp-like syntax: git@github.com:owner/repo.git
		url = strings.TrimPrefix(url, "git@")
		url = strings.Replace(url, ":", "/", 1)

	case strings.HasPrefix(url, "ssh://"):
		url = strings.TrimPrefix(url, "ssh://")
		url = strings.TrimPrefix(url, "git@")

	case strings.HasPrefix(url, "https://"):
		url = strings.TrimPrefix(url, "https://")
		// Drop any embedded credentials rather than carrying a token around.
		if i := strings.Index(url, "@"); i >= 0 {
			url = url[i+1:]
		}

	case strings.HasPrefix(url, "http://"):
		url = strings.TrimPrefix(url, "http://")
		if i := strings.Index(url, "@"); i >= 0 {
			url = url[i+1:]
		}
	}

	url = strings.TrimSuffix(url, "/")
	url = strings.TrimSuffix(url, ".git")

	parts := strings.Split(url, "/")
	if len(parts) < 3 {
		return Repo{}, fmt.Errorf("discover: cannot parse remote %q as host/owner/repo", original)
	}

	host := parts[0]
	// A host may carry a port, which is not part of its identity here.
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}

	name := parts[len(parts)-1]
	owner := parts[len(parts)-2]
	if host == "" || owner == "" || name == "" {
		return Repo{}, fmt.Errorf("discover: cannot parse remote %q as host/owner/repo", original)
	}

	return Repo{Host: host, Owner: owner, Name: name}, nil
}
