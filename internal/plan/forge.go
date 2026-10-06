package plan

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/danielriddell21/letsgo/internal/brew"
	"github.com/danielriddell21/letsgo/internal/config"
	"github.com/danielriddell21/letsgo/internal/publish/github"
)

func underActions() bool { return os.Getenv("GITHUB_ACTIONS") == "true" }

// unconfirmedUnderActions says what could not be established and where to
// look if the publish then fails, without asserting a permission is missing.
func unconfirmedUnderActions(repo string) string {
	return fmt.Sprintf(
		"write access to %s could not be confirmed\n"+
			"a workflow token's permissions are not described by the repository\n"+
			"endpoint, so this is not evidence that it lacks them\n"+
			"if publishing fails: check `permissions: contents: write` in the\n"+
			"workflow, and Settings \u2192 Actions \u2192 General \u2192 Workflow permissions",
		repo)
}

// noActionsWriteAccess explains a refusal in terms of where the permission is
// granted to a workflow, which is not where a token's scopes live.
//
// A workflow may request no more than the repository allows, so
// `permissions: contents: write` has no effect while the repository default is
// read-only — and that setting is several screens away from the workflow file
// the reader is looking at.
func noActionsWriteAccess(repo string) string {
	return fmt.Sprintf(
		"this workflow's token may not create releases in %s\n"+
			"check both: `permissions: contents: write` in the workflow, and\n"+
			"Settings \u2192 Actions \u2192 General \u2192 Workflow permissions\n"+
			"\u2192 \"Read and write permissions\"",
		repo)
}

// noWriteAccess explains a missing permission in terms of where it is granted.
// Only reached outside Actions, where the reported permission is trustworthy.
func noWriteAccess(source, repo string) string {
	return fmt.Sprintf(
		"%s cannot write to %s; a release needs contents:write\n"+
			"a fine-grained token needs the Contents repository permission set to\n"+
			"Read and write; a classic token needs the repo scope",
		source, repo)
}

// checkForge verifies, before anything is built, that there is somewhere to
// publish and permission to do it. One API call now is worth more than a
// perfect set of artifacts and a 401.
func (p *Plan) checkForge(ctx context.Context, opts Options) {
	if !p.HasRepo {
		p.add("forge", Fail, "no 'origin' remote, so there is nowhere to publish")
		return
	}
	if p.Repo.Host != "github.com" {
		p.add("forge", Fail, "%s is not supported yet; letsgo publishes to github.com", p.Repo.Host)
		return
	}

	token, source := Token(ctx, p.Global, opts.Token)
	if token == "" {
		p.add("token", Fail, "no token; set %s", strings.Join(TokenEnvVars, " or "))
		return
	}

	client := opts.client(token)
	repo := github.Repo{Owner: p.Repo.Owner, Name: p.Repo.Name}
	access, err := client.CheckAccess(ctx, repo)
	if err != nil {
		// Unreachable is definitive: the repository is private to this token,
		// renamed, or gone.
		p.add("token", Fail, "%v", err)
		return
	}
	if access.Archived {
		p.add("token", Fail, "%s is archived and cannot receive a release", p.Repo)
		return
	}
	p.note("token", source, tokenNoteFrom(source))

	if !p.checkRelease(ctx, opts, repo, token, client, access) {
		return
	}

	// The tap is probed with the credential that will actually write to it.
	// Probing the release's credential instead is how a plan passes and the
	// release then fails on its last step, which is the one failure this
	// gate exists to prevent.
	tapToken, tapSource := TapToken(ctx, p.Global, opts.TapToken, opts.Token)
	tapClient := client
	if tapToken != token {
		tapClient = opts.client(tapToken)
		p.note("tap token", tapSource, tokenNoteFrom(tapSource))
	}

	p.checkTap(ctx, tapClient, tapSource)
}

// client builds the forge client for token.
func (o Options) client(token string) *github.Client {
	if o.NewClient != nil {
		return o.NewClient(token)
	}
	return github.New(token)
}

// ClientAt returns a client factory for a forge served at endpoint, for the
// callers that stand one up in place of the real one.
func ClientAt(endpoint string) func(token string) *github.Client {
	return func(token string) *github.Client {
		c := github.New(token)
		c.SetEndpoints(endpoint, endpoint)
		return c
	}
}

// checkRelease establishes that the release can be created with whichever
// credential will actually create it — its own, if one is configured,
// otherwise the plain token — the same way checkTap establishes it for the
// formula. Probing the wrong one is how a plan passes and the release then
// fails on its last step, which is the one failure this gate exists to
// prevent.
//
// client and access are what checkForge already established for the plain
// token, reused when no release token is configured so the common case costs
// no second API call.
func (p *Plan) checkRelease(
	ctx context.Context, opts Options, repo github.Repo, token string, client *github.Client, access github.Access,
) bool {
	releaseToken, releaseSource := ReleaseToken(ctx, p.Global, opts.ReleaseToken, opts.Token)
	releaseClient, releaseAccess := client, access
	if releaseToken != token {
		releaseClient = opts.client(releaseToken)
		var err error
		releaseAccess, err = releaseClient.CheckAccess(ctx, repo)
		if err != nil {
			p.add("token", Fail, "%v", err)
			return false
		}
		if releaseAccess.Archived {
			p.add("token", Fail, "%s is archived and cannot receive a release", p.Repo)
			return false
		}
		p.note("release token", releaseSource, tokenNoteFrom(releaseSource))
	}

	switch {
	case releaseAccess.CanPush:
		p.add("token", Pass, "%s can write to %s", releaseSource, p.Repo)
		return true

	case !underActions():
		// For a user token the reported permission is accurate, so this is a
		// real answer and worth stopping for.
		p.add("token", Fail, "%s", noWriteAccess(releaseSource, p.Repo.String()))
		return false

	default:
		// A workflow token is an installation token, whose permissions the
		// repository endpoint does not describe. Ask the forge directly.
		allowed, err := releaseClient.CanCreateRelease(ctx, repo)
		switch {
		case err != nil:
			// Neither established nor refuted. Blocking here would refuse
			// correctly configured releases on no evidence, and the publish
			// attempt will give a definitive answer shortly.
			p.add("token", Warn, "%s", unconfirmedUnderActions(p.Repo.String()))
			return true
		case allowed:
			p.add("token", Pass, "%s may create releases in %s", releaseSource, p.Repo)
			return true
		default:
			p.add("token", Fail, "%s", noActionsWriteAccess(p.Repo.String()))
			return false
		}
	}
}

// resolveTap parses the configured Homebrew tap.
//
// Separate from checkTap so that a malformed tap is reported by a plain
// `letsgo plan`, which contacts nothing.
func (p *Plan) resolveTap() {
	if p.Config.BrewTap == "" {
		return
	}
	tap, err := brew.ParseTap(p.Config.BrewTap)
	if err != nil {
		p.addAt(p.posOf("brew"), brewTap, Fail, "%v", err)
		return
	}
	p.Tap = tap
	p.note(brewTap, tap.String(), config.FileName)
}

// checkTap establishes that the formula has somewhere to go before anything is
// built. A release that succeeds and then cannot update the tap has left the
// two out of step, which is worse than not starting.
func (p *Plan) checkTap(ctx context.Context, client *github.Client, source string) {
	if p.Tap == (github.Repo{}) {
		return
	}

	access, err := client.CheckAccess(ctx, p.Tap)
	switch {
	case err != nil:
		p.add(brewTap, Fail, "%v", err)
	case access.Archived:
		p.add(brewTap, Fail, "%s is archived and cannot receive a formula", p.Tap)
	case access.CanPush:
		p.add(brewTap, Pass, "%s can receive the formula, with %s", p.Tap, source)
	case underActions():
		// Same limitation as the release token: an installation token's
		// permissions are not described by the repository endpoint, and a
		// tap in another repository needs a token this one cannot inspect.
		p.add(brewTap, Warn,
			"whether %s can write to %s cannot be confirmed from inside Actions\n"+
				"  a workflow token cannot write to another repository; set %s to an App token scoped to the tap",
			source, p.Tap, TapTokenEnvVars[0])
	default:
		p.add(brewTap, Fail, "%s cannot write to %s", source, p.Tap)
	}
}
