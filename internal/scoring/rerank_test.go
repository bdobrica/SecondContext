package scoring

import (
	"testing"
	"time"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/models"
)

func TestRecencyScoreDecaysWithAge(t *testing.T) {
	now := time.Date(2026, time.May, 10, 10, 0, 0, 0, time.UTC)
	recent := RecencyScore(now.Add(-24*time.Hour), now, 30)
	older := RecencyScore(now.Add(-60*24*time.Hour), now, 30)
	if recent <= older {
		t.Fatalf("expected recent score > older score, got recent=%f older=%f", recent, older)
	}
}

func TestRankMemoriesAppliesSalienceAndRedundancy(t *testing.T) {
	now := time.Date(2026, time.May, 10, 10, 0, 0, 0, time.UTC)
	candidates := []Candidate{
		{
			Memory: models.MemoryItem{
				ID:           "old-best-match",
				Summary:      "Alex prefers tightly scoped API review requests",
				RawText:      "Alex prefers tightly scoped API review requests",
				MemoryType:   "person_preference",
				People:       []string{"Alex"},
				Topics:       []string{"api_review"},
				Importance:   0.25,
				Utility:      0.20,
				BeliefImpact: 0.10,
				Confidence:   0.35,
				CreatedAt:    now.Add(-90 * 24 * time.Hour),
			},
			RetrievalScore: 1.0,
			DenseScore:     1.0,
			SparseScore:    1.0,
			HybridScore:    1.0,
			Goal:           "api scoped review request",
		},
		{
			Memory: models.MemoryItem{
				ID:           "fresh-actionable",
				Summary:      "Alex wants scoped API reviews with action items and evidence",
				RawText:      "Alex wants scoped API reviews with action items and evidence",
				MemoryType:   "person_preference",
				People:       []string{"Alex"},
				Topics:       []string{"api_review"},
				Importance:   0.95,
				Utility:      0.95,
				BeliefImpact: 0.55,
				Confidence:   0.95,
				CreatedAt:    now.Add(-24 * time.Hour),
			},
			RetrievalScore: 0.72,
			DenseScore:     0.7,
			SparseScore:    0.6,
			HybridScore:    0.72,
			Goal:           "api scoped review request",
		},
		{
			Memory: models.MemoryItem{
				ID:           "duplicate-fresh",
				Summary:      "Alex wants scoped API reviews with action items and evidence",
				RawText:      "Alex wants scoped API reviews with action items and evidence",
				MemoryType:   "person_preference",
				People:       []string{"Alex"},
				Topics:       []string{"api_review"},
				Importance:   0.90,
				Utility:      0.90,
				BeliefImpact: 0.50,
				Confidence:   0.90,
				CreatedAt:    now.Add(-2 * time.Hour),
			},
			RetrievalScore: 0.70,
			DenseScore:     0.69,
			SparseScore:    0.61,
			HybridScore:    0.70,
			Goal:           "api scoped review request",
		},
	}

	ranked := RankMemories(candidates, now, config.ScoringConfig{
		RetrievalWeight:     0.35,
		RecencyWeight:       0.15,
		ImportanceWeight:    0.15,
		UtilityWeight:       0.15,
		GoalRelevanceWeight: 0.10,
		BeliefImpactWeight:  0.05,
		ConfidenceWeight:    0.05,
		RecencyHalfLifeDays: 30,
		RedundancyThreshold: 0.80,
	}, 5)

	if len(ranked) != 2 {
		t.Fatalf("expected 2 ranked memories after deduplication, got %d", len(ranked))
	}
	if ranked[0].Memory.ID != "fresh-actionable" {
		t.Fatalf("expected fresher higher-salience memory first, got %s", ranked[0].Memory.ID)
	}
	if ranked[0].Breakdown.Final <= ranked[1].Breakdown.Final {
		t.Fatalf("expected first score > second score, got %f <= %f", ranked[0].Breakdown.Final, ranked[1].Breakdown.Final)
	}
	if ranked[0].Breakdown.GoalRelevance == 0 {
		t.Fatal("expected non-zero goal relevance for first result")
	}
	if ranked[1].Breakdown.MaxSimilarityToHigherRanked == 0 {
		t.Fatal("expected second result to report similarity to the higher-ranked memory")
	}
}
