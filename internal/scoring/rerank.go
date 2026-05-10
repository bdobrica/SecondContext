package scoring

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/models"
)

var rerankTokenPattern = regexp.MustCompile(`[a-z0-9]+`)

type Candidate struct {
	Memory         models.MemoryItem
	RetrievalScore float64
	DenseScore     float64
	SparseScore    float64
	HybridScore    float64
	Goal           string
}

type Breakdown struct {
	Final                       float64
	Retrieval                   float64
	Recency                     float64
	Importance                  float64
	Utility                     float64
	GoalRelevance               float64
	BeliefImpact                float64
	Confidence                  float64
	MaxSimilarityToHigherRanked float64
}

type RankedMemory struct {
	Memory      models.MemoryItem
	Breakdown   Breakdown
	DenseScore  float64
	SparseScore float64
	HybridScore float64
	Rank        int
}

func RankMemories(candidates []Candidate, now time.Time, cfg config.ScoringConfig, limit int) []RankedMemory {
	if len(candidates) == 0 {
		return []RankedMemory{}
	}
	if limit <= 0 {
		limit = 10
	}

	weights := normalizedWeights(cfg)
	items := make([]RankedMemory, 0, len(candidates))
	for _, candidate := range candidates {
		breakdown := Breakdown{
			Retrieval:     clampUnit(candidate.RetrievalScore),
			Recency:       RecencyScore(candidate.Memory.CreatedAt, now, cfg.RecencyHalfLifeDays),
			Importance:    clampUnit(candidate.Memory.Importance),
			Utility:       clampUnit(candidate.Memory.Utility),
			GoalRelevance: GoalRelevance(candidate.Goal, candidate.Memory),
			BeliefImpact:  clampUnit(candidate.Memory.BeliefImpact),
			Confidence:    clampUnit(candidate.Memory.Confidence),
		}
		breakdown.Final =
			weights.RetrievalWeight*breakdown.Retrieval +
				weights.RecencyWeight*breakdown.Recency +
				weights.ImportanceWeight*breakdown.Importance +
				weights.UtilityWeight*breakdown.Utility +
				weights.GoalRelevanceWeight*breakdown.GoalRelevance +
				weights.BeliefImpactWeight*breakdown.BeliefImpact +
				weights.ConfidenceWeight*breakdown.Confidence

		items = append(items, RankedMemory{
			Memory:      candidate.Memory,
			Breakdown:   breakdown,
			DenseScore:  candidate.DenseScore,
			SparseScore: candidate.SparseScore,
			HybridScore: candidate.HybridScore,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Breakdown.Final == items[j].Breakdown.Final {
			if items[i].Breakdown.Retrieval == items[j].Breakdown.Retrieval {
				return items[i].Memory.CreatedAt.After(items[j].Memory.CreatedAt)
			}
			return items[i].Breakdown.Retrieval > items[j].Breakdown.Retrieval
		}
		return items[i].Breakdown.Final > items[j].Breakdown.Final
	})

	selected := make([]RankedMemory, 0, min(limit, len(items)))
	threshold := clampThreshold(cfg.RedundancyThreshold)
	for _, item := range items {
		maxSimilarity := 0.0
		for _, prior := range selected {
			similarity := SummarySimilarity(item.Memory, prior.Memory)
			if similarity > maxSimilarity {
				maxSimilarity = similarity
			}
		}
		if maxSimilarity >= threshold {
			continue
		}
		item.Breakdown.MaxSimilarityToHigherRanked = maxSimilarity
		item.Rank = len(selected) + 1
		selected = append(selected, item)
		if len(selected) >= limit {
			break
		}
	}

	return selected
}

func RecencyScore(createdAt, now time.Time, halfLifeDays float64) float64 {
	if createdAt.IsZero() {
		return 0
	}
	if halfLifeDays <= 0 {
		halfLifeDays = 30
	}
	age := now.Sub(createdAt)
	if age <= 0 {
		return 1
	}
	halfLife := halfLifeDays * 24 * float64(time.Hour)
	return math.Exp(-math.Ln2 * age.Seconds() / (halfLife / float64(time.Second)))
}

func GoalRelevance(goal string, memory models.MemoryItem) float64 {
	goalTokens := tokenSet(goal)
	if len(goalTokens) == 0 {
		return 0
	}
	memoryTokens := tokenSet(strings.Join([]string{
		memory.Summary,
		memory.RawText,
		strings.Join(memory.People, " "),
		strings.Join(memory.Topics, " "),
		memory.MemoryType,
	}, " "))
	if len(memoryTokens) == 0 {
		return 0
	}
	matched := 0
	for token := range goalTokens {
		if _, ok := memoryTokens[token]; ok {
			matched++
		}
	}

	return float64(matched) / float64(len(goalTokens))
}

func SummarySimilarity(left, right models.MemoryItem) float64 {
	leftTokens := tokenSet(left.Summary)
	rightTokens := tokenSet(right.Summary)
	if len(leftTokens) == 0 || len(rightTokens) == 0 {
		return 0
	}
	intersection := 0
	union := len(leftTokens)
	for token := range rightTokens {
		if _, ok := leftTokens[token]; ok {
			intersection++
			continue
		}
		union++
	}
	if union == 0 {
		return 0
	}

	return float64(intersection) / float64(union)
}

func tokenSet(text string) map[string]struct{} {
	tokens := rerankTokenPattern.FindAllString(strings.ToLower(text), -1)
	set := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		if len(token) < 2 {
			continue
		}
		set[token] = struct{}{}
	}

	return set
}

func normalizedWeights(cfg config.ScoringConfig) config.ScoringConfig {
	weights := config.ScoringConfig{
		RetrievalWeight:     positiveOr(cfg.RetrievalWeight, 0.35),
		RecencyWeight:       positiveOr(cfg.RecencyWeight, 0.15),
		ImportanceWeight:    positiveOr(cfg.ImportanceWeight, 0.15),
		UtilityWeight:       positiveOr(cfg.UtilityWeight, 0.15),
		GoalRelevanceWeight: positiveOr(cfg.GoalRelevanceWeight, 0.10),
		BeliefImpactWeight:  positiveOr(cfg.BeliefImpactWeight, 0.05),
		ConfidenceWeight:    positiveOr(cfg.ConfidenceWeight, 0.05),
	}
	total := weights.RetrievalWeight + weights.RecencyWeight + weights.ImportanceWeight + weights.UtilityWeight + weights.GoalRelevanceWeight + weights.BeliefImpactWeight + weights.ConfidenceWeight
	if total <= 0 {
		return config.ScoringConfig{
			RetrievalWeight:     0.35,
			RecencyWeight:       0.15,
			ImportanceWeight:    0.15,
			UtilityWeight:       0.15,
			GoalRelevanceWeight: 0.10,
			BeliefImpactWeight:  0.05,
			ConfidenceWeight:    0.05,
		}
	}

	weights.RetrievalWeight /= total
	weights.RecencyWeight /= total
	weights.ImportanceWeight /= total
	weights.UtilityWeight /= total
	weights.GoalRelevanceWeight /= total
	weights.BeliefImpactWeight /= total
	weights.ConfidenceWeight /= total
	weights.RecencyHalfLifeDays = cfg.RecencyHalfLifeDays
	weights.RedundancyThreshold = cfg.RedundancyThreshold

	return weights
}

func clampUnit(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}

	return value
}

func clampThreshold(value float64) float64 {
	if value <= 0 {
		return 0.82
	}
	if value > 1 {
		return 1
	}

	return value
}

func positiveOr(value, fallback float64) float64 {
	if value <= 0 {
		return fallback
	}

	return value
}

func min(left, right int) int {
	if left < right {
		return left
	}

	return right
}
