package bump

import (
	"encoding/json"
	"fmt"
)

// jsonResult is Proposal's wire form for `letsgo tag --json`: schema-versioned
// so a consumer can tell whether it understands a given output.
type jsonResult struct {
	Schema int `json:"schema"`
	Proposal
	Disagree bool `json:"disagree,omitempty"`

	// Ref is the tag the proposal names, scope prefix included, so a consumer
	// never rebuilds it from Next. Tagged says whether letsgo created it.
	Ref    string `json:"ref,omitempty"`
	Tagged bool   `json:"tagged,omitempty"`
}

// JSON renders the proposal as `letsgo tag --json` prints it: the same
// evidence reportProposal writes as text, in a schema-versioned wire form —
// a dry run unless tagged says the tag was created. ref is the full tag name.
func (p Proposal) JSON(ref string, tagged bool) ([]byte, error) {
	data, err := json.MarshalIndent(jsonResult{Schema: 1, Proposal: p, Disagree: p.Disagree(), Ref: ref, Tagged: tagged}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("bump: %w", err)
	}
	return data, nil
}
