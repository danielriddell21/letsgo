// letsgo.mod

// Everything else is deliberately absent. letsgo's own release runs on
// zero-config defaults — the same defaults every repository gets before it
// writes one of these — so the tool cannot quietly depend on configuration it
// does not require of anybody else.

// The formula is a third way in, alongside the install script and
// `go install`. letsgo writes it from the manifest it just published, so what
// brew installs is the archive the release attests to.
brew danielriddell21/tap
