package main

import (
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/internal/plan"
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

	// global is the machine's config, read once in main (see loadGlobal). Nil
	// is an empty one, which is what a test gets.
	global *config.Global

	// globalErr is why it could not be read, for plan to report.
	globalErr error
}

// machine is the machine's global config, never nil.
func (f forge) machine() *config.Global {
	if f.global == nil {
		return &config.Global{}
	}
	return f.global
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

// planOptions fills in what every plan the commands resolve shares: the
// machine's config as main read it, so the plan never reads it a second time,
// and the reason it could not, which the plan reports.
func (f forge) planOptions(o plan.Options) plan.Options {
	o.Global, o.GlobalErr = f.machine(), f.globalErr
	return o
}
