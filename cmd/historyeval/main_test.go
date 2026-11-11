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
