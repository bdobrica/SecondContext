package retrieval

import (
	"context"
	"errors"
	"math"
	"net/http"
	"testing"
)

func TestSearchLimitValidation(t *testing.T) {
	service := &Service{}
	for _, limit := range []int{-1, MaxSearchLimit + 1, math.MaxInt} {
		_, err := service.Search(context.Background(), SearchParams{Query: "test", Limit: limit})
		var serviceError *Error
		if !errors.As(err, &serviceError) || serviceError.StatusCode != http.StatusBadRequest || serviceError.Code != "invalid_limit" {
			t.Fatalf("limit %d error = %#v, want invalid_limit", limit, err)
		}
	}
}

func TestCandidateLimitExpansion(t *testing.T) {
	tests := []struct {
		limit int
		want  int
	}{
		{limit: 0, want: 40},
		{limit: 1, want: 20},
		{limit: MaxSearchLimit, want: MaxCandidateLimit},
		{limit: math.MaxInt, want: MaxCandidateLimit},
	}
	for _, test := range tests {
		if got := expandedLimit(test.limit); got != test.want {
			t.Errorf("expandedLimit(%d) = %d, want %d", test.limit, got, test.want)
		}
	}

	if got := expandedPrefetchLimit(math.MaxInt); got != MaxPrefetchLimit {
		t.Fatalf("expandedPrefetchLimit(MaxInt) = %d, want %d", got, MaxPrefetchLimit)
	}
}
