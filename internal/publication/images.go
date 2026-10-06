package publication

import (
	"context"
	"fmt"
	"io"

	"github.com/danielriddell21/letsgo/internal/github"
	"github.com/danielriddell21/letsgo/internal/release"
)

// publishImages pushes the container images the build assembled.
//
// A rehearsal prints what would be pushed and pushes nothing. It can be
// specific rather than hand-waving because the digests are already fixed:
// assembly happened during the build, so the rehearsal names the exact image a
// real run would publish.
func publishImages(ctx context.Context, out io.Writer, o Options) error {
	if len(o.Result.Images) == 0 {
		return nil
	}

	if o.Snapshot {
		fmt.Fprint(out, "\n  images that would be pushed\n")
		fmt.Fprint(out, release.Describe(o.Result.Images))
		return nil
	}
	if o.Plan.Config.Draft {
		fmt.Fprintln(out, "  ! skipped the container image: a draft release should not publish a public tag")
		return nil
	}

	return PushImages(ctx, o.Result.Images, o.Token, func(format string, args ...any) {
		fmt.Fprintf(out, "  "+format+"\n", args...)
	})
}

// ReleaseRepoInfo is what the forge said about the repository, in the
// forge-neutral form the build takes. nil, as when nothing was read, is the
// zero RepoInfo.
func ReleaseRepoInfo(info *github.RepoInfo) release.RepoInfo {
	if info == nil {
		return release.RepoInfo{}
	}
	return release.RepoInfo{Description: info.Description, License: info.License, Homepage: info.Homepage}
}
