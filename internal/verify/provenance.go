package verify

import (
	"context"

	"github.com/danielriddell21/letsgo/internal/manifest"
)

// checkProvenance asks the forge what it has recorded about how each artifact
// was produced.
//
// Provenance answers a question reproducibility cannot: rebuilding proves the
// bytes follow from the source, but not that the release was published by the
// workflow it claims to come from. The attestation is signed at build time by
// the forge's own identity, so it ties the artifact to a repository, a
// workflow and a commit without anyone holding a key.
//
// Its absence is reported rather than failed. A release without provenance is
// still verifiable by rebuilding; it simply carries one guarantee fewer, and
// that is worth knowing rather than being stopped by.
func checkProvenance(ctx context.Context, o Options, result *Result, m *manifest.Manifest) {
	attested, missing := 0, 0

	for _, a := range m.Artifacts {
		attestations, err := o.Client.Attestations(ctx, o.Repo, a.SHA256)
		if err != nil {
			result.add("provenance", Warn, "could not be read: %v", err)
			return
		}
		if len(attestations) > 0 {
			attested++
			continue
		}
		missing++
	}

	switch {
	case attested == 0:
		result.add("provenance", Warn, "%s",
			"no attestations; this release does not record which workflow built it")
	case missing > 0:
		result.add("provenance", Warn, "%d of %d artifacts carry an attestation",
			attested, attested+missing)
	default:
		result.add("provenance", Pass, "all %d artifacts are attested to %s",
			attested, o.Repo)
	}
}
