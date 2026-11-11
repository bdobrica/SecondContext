package decisions

import "encoding/json"

// GenerationUsage requires cache accounting before estimating combined cost.
func GenerationUsage(body []byte) *Usage {
	var wire struct {
		Usage *struct {
			Input   *int `json:"prompt_tokens"`
			Output  *int `json:"completion_tokens"`
			Total   *int `json:"total_tokens"`
			Details *struct {
				Cached *int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &wire) != nil {
		return nil
	}
	u := wire.Usage
	if u == nil || u.Input == nil || u.Output == nil || u.Total == nil || u.Details == nil || u.Details.Cached == nil {
		return nil
	}
	if *u.Input < 0 || *u.Input > 10000000 || *u.Output < 0 || *u.Output > 10000000 || *u.Total != *u.Input+*u.Output || *u.Details.Cached < 0 || *u.Details.Cached > *u.Input {
		return nil
	}
	out := &Usage{Input: *u.Input, Output: *u.Output, Total: *u.Total}
	out.Details.Cached = *u.Details.Cached
	return out
}
