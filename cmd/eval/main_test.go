package main

import "testing"

func TestLoadDataset(t *testing.T) {
	dataset, err := loadDataset()
	if err != nil {
		t.Fatalf("load dataset: %v", err)
	}
	if dataset.Name == "" {
		t.Fatal("expected dataset name")
	}
	if len(dataset.Cases) < 2 {
		t.Fatalf("expected multiple evaluation cases, got %d", len(dataset.Cases))
	}
	for _, currentCase := range dataset.Cases {
		if currentCase.ID == "" || currentCase.Title == "" {
			t.Fatalf("expected case id and title, got %#v", currentCase)
		}
		if len(currentCase.ExpectedMemoryKeys) == 0 {
			t.Fatalf("expected expected memory keys for case %s", currentCase.ID)
		}
	}
}

func TestCueHits(t *testing.T) {
	hits, misses := cueHits("Ask Alex for an API-only review and send Dana a quantified risk summary.", []string{"Alex", "API-only", "quantified risk", "migration board"})
	if hits != 3 {
		t.Fatalf("expected 3 hits, got %d", hits)
	}
	if len(misses) != 1 || misses[0] != "migration board" {
		t.Fatalf("unexpected misses %#v", misses)
	}
}
