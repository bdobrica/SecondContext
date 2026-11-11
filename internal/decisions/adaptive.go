package decisions

// AdaptiveRecipe is pinned in pending-turn configuration bindings. The stricter
// threshold is independent of the observer's diagnostic 0.2/0.8 buckets.
const AdaptiveRecipe = "standalone-1"
const StandaloneThreshold = 0.05

// OmitRecent requires BOTH dependencies to be confidently absent. Older signals,
// ambiguity, refusal and errors retain recent history. This grants no authority.
func OmitRecent(r Result) bool {
	return r.Outcome == "success" && r.Recent != nil && r.Older != nil &&
		*r.Recent >= 0 && *r.Recent <= StandaloneThreshold &&
		*r.Older >= 0 && *r.Older <= StandaloneThreshold
}
