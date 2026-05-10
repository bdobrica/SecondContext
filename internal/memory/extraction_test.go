package memory

import "testing"

func TestParseAndValidateExtractionRepairableJSON(t *testing.T) {
	raw := "```json\n{\"summary\":\"Alex prefers narrow review scopes.\",\"type\":\"person_preference\",\"people\":[\"Alex\"],\"topics\":[\"infrastructure\"],\"entities\":[{\"type\":\"person\",\"name\":\"Alex\",\"confidence\":0.9},{\"type\":\"topic\",\"name\":\"Infrastructure\",\"confidence\":0.8}],\"importance\":0.7,\"utility\":0.8,\"belief_impact\":0.2,\"confidence\":0.9,\"expires_in_days\":30}\n```"

	extraction, err := parseAndValidateExtraction(raw)
	if err != nil {
		t.Fatalf("parse extraction: %v", err)
	}

	if extraction.Type != "person_preference" {
		t.Fatalf("unexpected type %q", extraction.Type)
	}
	if len(extraction.Entities) != 2 {
		t.Fatalf("unexpected entities %#v", extraction.Entities)
	}
}

func TestParseAndValidateExtractionRejectsUnknownType(t *testing.T) {
	raw := "{\"summary\":\"Bad type\",\"type\":\"made_up\",\"people\":[],\"topics\":[],\"entities\":[],\"importance\":0.1,\"utility\":0.2,\"belief_impact\":0.0,\"confidence\":0.3}"

	if _, err := parseAndValidateExtraction(raw); err == nil {
		t.Fatal("expected validation error")
	}
}
