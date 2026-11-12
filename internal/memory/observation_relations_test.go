package memory

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestObservationRelationsValidation(t *testing.T) {
	o := Observation{Summary: "Alex prefers a detailed review.", Quote: "I was mistaken: Alex prefers a detailed review.", Type: "person_preference", People: []string{"Alex"}, TimeScope: "now"}
	candidate := ObservationCandidate{ID: "prior", Summary: "Alex prefers short reviews.", Type: "person_preference", People: []string{"Alex"}}
	valid := `{"relations":[{"id":"prior","kind":"correction","prior_quote":"prefers short reviews","new_quote":"I was mistaken","reason":"The user explicitly retracts the earlier preference."}]}`
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"correction", valid, true},
		{"agreement", strings.Replace(valid, "correction", "agreement", 1), true},
		{"contradiction", strings.Replace(valid, "correction", "contradiction", 1), true},
		{"time change", strings.Replace(valid, "correction", "changed_circumstances", 1), true},
		{"missing decision", `{"relations":[]}`, false},
		{"null", `{"relations":null}`, false},
		{"unknown ID", strings.Replace(valid, `"prior"`, `"invented"`, 1), false},
		{"invented quote", strings.Replace(valid, "I was mistaken", "Alex agreed", 1), false},
		{"extra authority", strings.Replace(valid, `"id":`, `"delete":true,"id":`, 1), false},
		{"wrong kind", strings.Replace(valid, "correction", "merge", 1), false},
		{"duplicate member", strings.Replace(valid, `"kind":`, `"kind":"agreement","kind":`, 1), false},
		{"trailing JSON", valid + ` {}`, false},
		{"outcome from preference", strings.Replace(valid, "correction", "outcome_of", 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseObservationRelations(tc.raw, o, []ObservationCandidate{candidate})
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
	for _, name := range []string{"Alex Chen", "Alex Morgan", "Lex"} {
		other := candidate
		other.People = []string{name}
		relations, err := ParseObservationRelations(valid, o, []ObservationCandidate{other})
		if err != nil || len(relations) != 0 {
			t.Fatalf("identity merge: %s %#v %v", name, relations, err)
		}
	}
	o.TimeScope = ""
	if _, err := ParseObservationRelations(strings.Replace(valid, "correction", "changed_circumstances", 1), o, []ObservationCandidate{candidate}); err == nil {
		t.Fatal("undated change accepted")
	}
	o.Type = "outcome"
	candidate.Type = "reported_action"
	linked := strings.Replace(valid, "correction", "outcome_of", 1)
	if _, err := ParseObservationRelations(linked, o, []ObservationCandidate{candidate}); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Relations []ObservationRelation `json:"relations"`
	}
	_ = json.Unmarshal([]byte(linked), &envelope)
	duplicate := envelope.Relations[0]
	duplicate.ID = "other"
	envelope.Relations = append(envelope.Relations, duplicate)
	second := candidate
	second.ID = "other"
	raw, _ := json.Marshal(envelope)
	if _, err := ParseObservationRelations(string(raw), o, []ObservationCandidate{candidate, second}); err == nil {
		t.Fatal("ambiguous action links accepted")
	}
}
