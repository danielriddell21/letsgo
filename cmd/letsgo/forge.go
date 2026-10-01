package main

import (
	"github.com/danielriddell21/letsgo/internal/publish/github"
)

// forge is where the commands that talk to a forge reach it. It is built once,
// in main, and passed down: a command never builds a client from a global, so
// a test can stand a fake forge up beside the real wiring instead of
// patching it.
//
// The zero value is the real forge.
type forge struct {
	// endpoint overrides the forge API host. Empty means the real one.
	endpoint string
}

// client is the forge client for a token. Every client a run makes, whichever
// credential it carries, is built here, so they all point at the same forge
// and identify the same way.
func (f forge) client(token string) *github.Client {
	client := github.New(token)
	client.UserAgent = "letsgo/" + version
	if f.endpoint != "" {
		client.SetEndpoints(f.endpoint, f.endpoint)
	}
	return client
}
