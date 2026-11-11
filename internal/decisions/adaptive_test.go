package decisions

import (
	"math"
	"testing"
)

func TestOmitRecent(t *testing.T) {
	for _, tc := range []struct {
		recent, older float64
		outcome       string
		omit          bool
	}{
		{0, 0, "success", true}, {.05, .05, "success", true}, {.05001, 0, "success", false},
		{0, .05001, "success", false}, {.01, .9, "success", false}, {.9, .01, "success", false},
		{.01, .01, "uncertain", false}, {.01, .01, "refusal", false}, {.01, .01, "timeout", false},
		{-1, 0, "success", false}, {math.NaN(), 0, "success", false}, {0, math.Inf(1), "success", false},
	} {
		if OmitRecent(Result{Recent: &tc.recent, Older: &tc.older, Outcome: tc.outcome}) != tc.omit {
			t.Fatalf("%+v", tc)
		}
	}
	if OmitRecent(Result{Outcome: "success"}) {
		t.Fatal("missing probabilities")
	}
}
