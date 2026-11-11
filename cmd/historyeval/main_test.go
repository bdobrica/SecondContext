package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSyntheticCorpus(t *testing.T) {
	var cases []evalCase
	if err := json.Unmarshal(corpus, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 10 {
		t.Fatal("missing evaluation categories")
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if c.Name == "" || seen[c.Name] || c.Current == "" {
			t.Fatal("invalid case")
		}
		seen[c.Name] = true
		if strings.Contains(c.Name, "handoff") && (strings.Contains(c.Current, "birth date") || strings.Contains(c.Current, "2000-")) {
			t.Fatal("private input in fixture")
		}
		for i, e := range c.History {
			if e.Sequence != int64(i+1) || e.User == "" || e.Assistant == "" {
				t.Fatal("invalid exchange")
			}
		}
	}
	if !topicCheck("Water droplets refract light", []string{"light"}, []string{"compatibility"}) || topicCheck("Tomorrow's compatibility", []string{"light"}, []string{"compatibility"}) {
		t.Fatal("topic heuristic")
	}
}

func TestAdaptiveCorpusAndGrounding(t *testing.T) {
	var cases []adaptiveCase
	if err := json.Unmarshal(adaptiveCorpus, &cases); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	seen := map[string]bool{}
	for _, tc := range cases {
		counts[tc.Split]++
		if seen[tc.Name] || tc.Name == "" || len(tc.History) == 0 || tc.Current == "" {
			t.Fatal("invalid authored case")
		}
		seen[tc.Name] = true
		if len(tc.RequiredAll) == 0 && len(tc.RequiredTerms) == 0 {
			t.Fatal("case lacks grounding assertion", tc.Name)
		}
		for i, ex := range tc.History {
			if ex.Sequence != int64(i+1) || ex.User == "" || ex.Assistant == "" {
				t.Fatal("invalid history")
			}
		}
		all := evalMessages(tc, false)
		omitted := evalMessages(tc, true)
		if len(omitted) != 2 || omitted[0] != all[0] || omitted[1] != all[len(all)-1] || len(all) > 18 {
			t.Fatal("incorrect prompt selection")
		}
	}
	if counts["calibration"] != 12 || counts["holdout"] != 12 {
		t.Fatal("split drift", counts)
	}
	tc := adaptiveCase{RequiredAll: []string{"Maple", "9"}}
	if groundedCheck("Maple has 4 places", tc) || !groundedCheck("Maple has 9 places", tc) {
		t.Fatal("fact rubric")
	}
}
