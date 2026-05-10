package retrieval

import (
	"hash/fnv"
	"regexp"
	"sort"
	"strings"

	"github.com/bdobrica/SecondContext/internal/qdrant"
)

var tokenPattern = regexp.MustCompile(`[a-z0-9]+`)

func BuildSparseVector(text string) qdrant.SparseVector {
	tokens := tokenPattern.FindAllString(strings.ToLower(text), -1)
	weights := make(map[uint32]float64)
	for _, token := range tokens {
		if len(token) < 2 {
			continue
		}
		weights[hashToken(token)] += 1
	}

	indices := make([]uint32, 0, len(weights))
	for index := range weights {
		indices = append(indices, index)
	}
	sort.Slice(indices, func(i, j int) bool { return indices[i] < indices[j] })

	values := make([]float64, 0, len(indices))
	for _, index := range indices {
		values = append(values, weights[index])
	}

	return qdrant.SparseVector{Indices: indices, Values: values}
}

func hashToken(token string) uint32 {
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(token))
	return hasher.Sum32()
}
