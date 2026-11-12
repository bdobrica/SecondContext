package memory

import (
	"encoding/json"
	"testing"
)

func TestMeetingObservationEvidence(t *testing.T) {
	source := "Alex: I propose moving the API review to Friday."
	valid := Observation{Summary: "Alex proposed moving the API review to Friday.", Type: "proposal", Quote: source, Evidence: "meeting_note", Speaker: "Alex", People: []string{"Alex"}, Topics: []string{"API"}}
	encode := func(o Observation) string {
		b, _ := json.Marshal(map[string]any{"observations": []Observation{o}})
		return string(b)
	}
	if _, err := parseObservations(encode(valid), source, true); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseObservations(encode(valid), source); err == nil {
		t.Fatal("note accepted as direct user report")
	}
	for _, change := range []func(*Observation){
		func(o *Observation) { o.Speaker = "Sam" },
		func(o *Observation) { o.Summary = "The review is on Friday." },
		func(o *Observation) { o.Evidence = "explicit_report" },
		func(o *Observation) { o.Quote = "Alex approved Friday." },
		func(o *Observation) { o.Speaker = "" },
	} {
		o := valid
		change(&o)
		if _, err := parseObservations(encode(o), source, true); err == nil {
			t.Fatalf("accepted unsupported note: %+v", o)
		}
	}
	if _, err := parseObservations(`{"observations":[]}`, source, true); err != nil {
		t.Fatal(err)
	}
}
