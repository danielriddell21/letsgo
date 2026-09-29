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
}

// JSON renders the proposal as `letsgo tag --json` prints it: the same
// evidence reportProposal writes as text, in a schema-versioned wire form —
// a dry run, since it never creates the tag itself.
func (p Proposal) JSON() ([]byte, error) {
	data, err := json.MarshalIndent(jsonResult{Schema: 1, Proposal: p, Disagree: p.Disagree()}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("bump: %w", err)
	}
	return data, nil
}
