package memory

import (
	"strings"
	"testing"
)

const validObservation = `{"summary":"Alex prefers small reviews.","type":"person_preference","quote":"Alex prefers small reviews.","evidence":"explicit_report","time_scope":"","expires_at":null,"people":["Alex"],"topics":[]}`

func TestObservationValidation(t *testing.T) {
	source := "Alex prefers small reviews. The deadline is Friday."
	for _, tc := range []struct {
		name, raw string
		count     int
		valid     bool
	}{
		{"none", `{"observations":[]}`, 0, true},
		{"one", `{"observations":[` + validObservation + `]}`, 1, true},
		{"multiple", `{"observations":[` + validObservation + `,{"summary":"Deadline Friday.","type":"constraint","quote":"The deadline is Friday.","evidence":"explicit_report","time_scope":"Friday","expires_at":"2026-11-14T00:00:00Z","people":[],"topics":[]}]}`, 2, true},
		{"missing array", `{}`, 0, false},
		{"null array", `{"observations":null}`, 0, false},
		{"invented source", `{"observations":[` + strings.ReplaceAll(validObservation, "Alex prefers small reviews.", "Alex sent the review.") + `]}`, 0, false},
		{"inference", `{"observations":[` + strings.ReplaceAll(validObservation, "explicit_report", "inference") + `]}`, 0, false},
		{"invented person", `{"observations":[` + strings.ReplaceAll(validObservation, `["Alex"]`, `["Sam"]`) + `]}`, 0, false},
		{"missing scope", `{"observations":[` + strings.ReplaceAll(validObservation, `"time_scope":"",`, ``) + `]}`, 0, false},
		{"duplicate member", `{"observations":[],"observations":[]}`, 0, false},
		{"trailing JSON", `{"observations":[]} {}`, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseObservations(tc.raw, source)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if tc.valid && len(got) != tc.count {
				t.Fatalf("got %#v", got)
			}
		})
	}
}
