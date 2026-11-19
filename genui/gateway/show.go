package gateway

import (
	"encoding/json"

	"github.com/hollis-labs/substrate/genui"
)

// decodeShow is shared by both raw adapters. Check exact required field names
// before decoding values: encoding/json's struct matching is case-insensitive
// even with DisallowUnknownFields, unlike the published tool schema.
func decodeShow(raw []byte) (ShowRequest, error) {
	if len(raw) > genui.MaxActionBytes {
		return ShowRequest{}, ErrInvalid
	}
	canonical, err := genui.CanonicalJSON(raw)
	if err != nil {
		return ShowRequest{}, ErrInvalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(canonical, &fields) != nil || len(fields) != 2 {
		return ShowRequest{}, ErrInvalid
	}
	pattern, patternOK := fields["pattern"]
	params, paramsOK := fields["params"]
	if !patternOK || !paramsOK || len(params) == 0 || params[0] != '{' {
		return ShowRequest{}, ErrInvalid
	}
	var q ShowRequest
	if json.Unmarshal(pattern, &q.Pattern) != nil {
		return ShowRequest{}, ErrInvalid
	}
	switch q.Pattern {
	case genui.PatternApproval, genui.PatternForm, genui.PatternListDetail:
	default:
		return ShowRequest{}, ErrInvalid
	}
	q.Params = params
	return q, nil
}
