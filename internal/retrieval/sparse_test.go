package retrieval

import "testing"

func TestBuildSparseVectorProducesStableTokenWeights(t *testing.T) {
	vector := BuildSparseVector("API review api scope")
	if len(vector.Indices) == 0 || len(vector.Indices) != len(vector.Values) {
		t.Fatalf("unexpected sparse vector %#v", vector)
	}

	if len(vector.Indices) != 3 {
		t.Fatalf("expected 3 unique tokens, got %#v", vector)
	}
}
