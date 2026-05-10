package retrieval

import (
	"testing"

	"github.com/bdobrica/SecondContext/internal/qdrant"
)

func TestBuildSparseVectorProducesStableTokenWeights(t *testing.T) {
	vector := BuildSparseVector("API review api scope")
	if len(vector.Indices) == 0 || len(vector.Indices) != len(vector.Values) {
		t.Fatalf("unexpected sparse vector %#v", vector)
	}

	if len(vector.Indices) != 3 {
		t.Fatalf("expected 3 unique tokens, got %#v", vector)
	}
}

func TestBuildFilterRelaxesPeopleForMixedProjectStateQueries(t *testing.T) {
	filter := buildFilter("user-1", SearchParams{
		Query:  "Summarize the current migration risk for the steering committee and tell me what to emphasize to Dana.",
		Goal:   "assess_risk",
		People: []string{"Dana"},
		Topics: []string{"migration", "risk_quantification"},
	})

	if hasFilterKey(filter, "people") {
		t.Fatalf("expected mixed project-state query to avoid hard people filter, got %#v", filter)
	}
	if !hasFilterKey(filter, "topics") {
		t.Fatalf("expected topics filter to remain present, got %#v", filter)
	}
}

func TestBuildFilterKeepsPeopleForPersonCentricQueries(t *testing.T) {
	filter := buildFilter("user-1", SearchParams{
		Query:  "Help me ask Alex to review the infrastructure proposal this week.",
		Goal:   "get_review",
		People: []string{"Alex", "Dana"},
		Topics: []string{"api_review", "infrastructure_proposal"},
	})

	if !hasFilterKey(filter, "people") {
		t.Fatalf("expected person-centric query to keep people filter, got %#v", filter)
	}
}

func hasFilterKey(filter *qdrant.Filter, key string) bool {
	if filter == nil {
		return false
	}
	for _, condition := range filter.Must {
		value, ok := condition["key"].(string)
		if ok && value == key {
			return true
		}
	}

	return false
}
