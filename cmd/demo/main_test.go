package main

import "testing"

func TestLoadScenario(t *testing.T) {
	scenario, err := loadScenario()
	if err != nil {
		t.Fatalf("load scenario: %v", err)
	}
	if scenario.ScenarioName == "" {
		t.Fatal("expected scenario name")
	}
	if len(scenario.Memories) < 4 {
		t.Fatalf("expected several seed memories, got %d", len(scenario.Memories))
	}
	if len(scenario.PeopleModels) == 0 {
		t.Fatal("expected seeded people models")
	}
	if len(scenario.Beliefs) == 0 {
		t.Fatal("expected seeded beliefs")
	}
}
