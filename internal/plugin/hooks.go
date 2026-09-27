package plugin

import pub "github.com/danielriddell21/letsgo/plugin"

// The wire types and the closed set of hooks live in the public
// github.com/danielriddell21/letsgo/plugin package, which is what a plugin
// binary imports. Aliased here so core's own code — plan.go, cmd/letsgo —
// keeps writing plugin.LDFlagsInput and the rest without a second copy of any
// of it to drift out of step.

type (
	LDFlagsInput        = pub.LDFlagsInput
	LDFlagsOutput       = pub.LDFlagsOutput
	ArchiveLayoutInput  = pub.ArchiveLayoutInput
	InputCommand        = pub.InputCommand
	ArchiveLayoutOutput = pub.ArchiveLayoutOutput
	OutputArchive       = pub.OutputArchive
)
