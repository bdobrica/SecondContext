package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"github.com/bdobrica/SecondContext/internal/api"
	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	demoBaseURLEnv     = "SECOND_CONTEXT_BASE_URL"
	demoCollectionPref = "demo"
	demoSourceLabel    = "demo.end_to_end"
	demoModel          = "context-agent-1"
)

//go:embed seed.json
var demoSeedJSON []byte

type demoScenario struct {
	ScenarioName         string            `json:"scenario_name"`
	UserExternalIDPrefix string            `json:"user_external_id_prefix"`
	UserDisplayName      string            `json:"user_display_name"`
	Query                string            `json:"query"`
	Goal                 string            `json:"goal"`
	People               []string          `json:"people"`
	Topics               []string          `json:"topics"`
	StrategyInstructions string            `json:"strategy_instructions"`
	OutcomeText          string            `json:"outcome_text"`
	FollowUpQuery        string            `json:"follow_up_query"`
	Memories             []seedMemory      `json:"memories"`
	PeopleModels         []seedPersonModel `json:"people_models"`
	Beliefs              []seedBelief      `json:"beliefs"`
}

type seedMemory struct {
	Key          string   `json:"key"`
	RawText      string   `json:"raw_text"`
	Summary      string   `json:"summary"`
	Type         string   `json:"type"`
	Source       string   `json:"source"`
	People       []string `json:"people"`
	Topics       []string `json:"topics"`
	Importance   float64  `json:"importance"`
	Utility      float64  `json:"utility"`
	BeliefImpact float64  `json:"belief_impact"`
	Confidence   float64  `json:"confidence"`
}

type seedPersonModel struct {
	PersonName         string   `json:"person_name"`
	PersonAliases      []string `json:"person_aliases"`
	TopicName          string   `json:"topic_name"`
	TopicAliases       []string `json:"topic_aliases"`
	Niceness           float64  `json:"niceness"`
	Readiness          float64  `json:"readiness"`
	Competence         float64  `json:"competence"`
	Capacity           float64  `json:"capacity"`
	Confidence         float64  `json:"confidence"`
	EvidenceMemoryKeys []string `json:"evidence_memory_keys"`
}

type seedBelief struct {
	TopicName          string   `json:"topic_name"`
	TopicAliases       []string `json:"topic_aliases"`
	Claim              string   `json:"claim"`
	Stance             string   `json:"stance"`
	Confidence         float64  `json:"confidence"`
	EvidenceMemoryKeys []string `json:"evidence_memory_keys"`
}

type createResponseRequest struct {
	Model         string         `json:"model"`
	Input         string         `json:"input"`
	Instructions  string         `json:"instructions,omitempty"`
	DisableMemory bool           `json:"disable_memory,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	User          string         `json:"user,omitempty"`
}

type createResponseResult struct {
	OutputText string                     `json:"output_text"`
	Metadata   map[string]json.RawMessage `json:"metadata,omitempty"`
}

type ingestMemoryRequest struct {
	RawText      string         `json:"raw_text"`
	Summary      string         `json:"summary"`
	Type         string         `json:"type"`
	Source       string         `json:"source,omitempty"`
	People       []string       `json:"people,omitempty"`
	Topics       []string       `json:"topics,omitempty"`
	Importance   float64        `json:"importance"`
	Utility      float64        `json:"utility"`
	BeliefImpact float64        `json:"belief_impact"`
	Confidence   float64        `json:"confidence"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	User         string         `json:"user,omitempty"`
}

type memoryResponse struct {
	ID         string   `json:"id"`
	MemoryType string   `json:"type"`
	Summary    string   `json:"summary"`
	People     []string `json:"people"`
	Topics     []string `json:"topics"`
	Confidence float64  `json:"confidence"`
}

type memorySearchRequest struct {
	Query          string   `json:"query"`
	Goal           string   `json:"goal,omitempty"`
	UserExternalID string   `json:"user_external_id,omitempty"`
	People         []string `json:"people,omitempty"`
	Topics         []string `json:"topics,omitempty"`
	Limit          int      `json:"limit,omitempty"`
}

type memorySearchResponse struct {
	Data []memorySearchResult `json:"data"`
}

type memorySearchResult struct {
	Rank   int              `json:"rank"`
	Memory memoryResponse   `json:"memory"`
	Scores memoryScoreBreak `json:"scores"`
}

type memoryScoreBreak struct {
	Final         float64 `json:"final"`
	Retrieval     float64 `json:"retrieval"`
	GoalRelevance float64 `json:"goal_relevance"`
	Recency       float64 `json:"recency"`
}

type createOutcomeRequest struct {
	SessionID string         `json:"session_id,omitempty"`
	RawText   string         `json:"raw_text"`
	Goal      string         `json:"goal,omitempty"`
	People    []string       `json:"people,omitempty"`
	Topics    []string       `json:"topics,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	User      string         `json:"user,omitempty"`
}

type createOutcomeResponse struct {
	Outcome  outcomeResponse `json:"outcome"`
	Memory   memoryResponse  `json:"memory"`
	Analysis struct {
		Summary         string  `json:"summary"`
		SuccessScore    float64 `json:"success_score"`
		PredictionError string  `json:"prediction_error"`
	} `json:"analysis"`
}

type outcomeResponse struct {
	SuccessScore    float64 `json:"success_score"`
	PredictionError string  `json:"prediction_error"`
	ActualOutcome   string  `json:"actual_outcome"`
}

type debugContextResponse struct {
	Session struct {
		ExternalID         string `json:"external_id"`
		AssistantMessageID string `json:"assistant_message_id"`
	} `json:"session"`
	StoredContextPacket  *contextPacket          `json:"stored_context_packet,omitempty"`
	CurrentContextPacket *contextPacket          `json:"current_context_packet,omitempty"`
	ScenarioPlan         *scenarioPlan           `json:"scenario_plan,omitempty"`
	PeopleModels         []personDebugResponse   `json:"people_models,omitempty"`
	RelevantBeliefs      []beliefDebugResponse   `json:"relevant_beliefs,omitempty"`
	LatestTurnUpdates    debugLatestTurnResponse `json:"latest_turn_updates,omitempty"`
}

type contextPacket struct {
	MemoryContext []contextMemory `json:"memory_context,omitempty"`
	PeopleContext []string        `json:"people_context,omitempty"`
	TopicContext  []string        `json:"topic_context,omitempty"`
	BeliefContext []string        `json:"belief_context,omitempty"`
}

type contextMemory struct {
	Summary string `json:"summary"`
	Type    string `json:"type"`
	Scores  struct {
		Final         float64 `json:"final"`
		Retrieval     float64 `json:"retrieval"`
		GoalRelevance float64 `json:"goal_relevance"`
		Recency       float64 `json:"recency"`
	} `json:"scores"`
}

type scenarioPlan struct {
	RecommendedStrategyID   string             `json:"recommended_strategy_id"`
	RecommendationRationale string             `json:"recommendation_rationale"`
	Strategies              []scenarioStrategy `json:"strategies"`
}

type scenarioStrategy struct {
	ID                  string   `json:"id"`
	Label               string   `json:"label"`
	MessageDraft        string   `json:"message_draft"`
	PredictedResponse   string   `json:"predicted_response"`
	Benefits            []string `json:"benefits"`
	Risks               []string `json:"risks"`
	LikelihoodOfSuccess float64  `json:"likelihood_of_success"`
	FallbackOption      string   `json:"fallback_option"`
}

type personDebugResponse struct {
	Name   string                    `json:"name"`
	Models []personTopicModelSummary `json:"models"`
}

type personTopicModelSummary struct {
	TopicName   string   `json:"topic_name"`
	Readiness   float64  `json:"readiness"`
	Competence  float64  `json:"competence"`
	Capacity    float64  `json:"capacity"`
	Confidence  float64  `json:"confidence"`
	Summary     string   `json:"summary"`
	EvidenceIDs []string `json:"evidence_memory_ids,omitempty"`
}

type beliefDebugResponse struct {
	TopicName  string  `json:"topic_name"`
	Claim      string  `json:"claim"`
	Stance     string  `json:"stance"`
	Confidence float64 `json:"confidence"`
	Summary    string  `json:"summary"`
}

type debugLatestTurnResponse struct {
	Outcome    *outcomeResponse `json:"outcome,omitempty"`
	Memories   []memoryResponse `json:"memories,omitempty"`
	GraphEdges []struct {
		Relationship string `json:"relationship"`
		SourceName   string `json:"source_name"`
		TargetName   string `json:"target_name"`
	} `json:"graph_edges,omitempty"`
}

type demoClient struct {
	baseURL    string
	httpClient *http.Client
}

type demoSeedState struct {
	Memories map[string]memoryResponse
}

type demoHTTPError struct {
	StatusCode int
	Body       string
}

func (e *demoHTTPError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("unexpected status %d: %s", e.StatusCode, strings.TrimSpace(e.Body))
}

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if !cfg.Postgres.Enabled {
		return fmt.Errorf("POSTGRES_ENABLED must be true for the demo")
	}

	scenario, err := loadScenario()
	if err != nil {
		return err
	}

	pool, err := db.Open(ctx, cfg.Postgres)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer db.Close(pool)

	runID := time.Now().UTC().Format("20060102t150405")
	if strings.TrimSpace(os.Getenv(demoBaseURLEnv)) == "" {
		cfg.Qdrant.Collection = fmt.Sprintf("%s_%s", demoCollectionPref, runID)
	}
	userExternalID := fmt.Sprintf("%s-%s", scenario.UserExternalIDPrefix, runID)
	userDisplayName := scenario.UserDisplayName
	if strings.TrimSpace(userDisplayName) == "" {
		userDisplayName = "Demo User"
	}
	userEmail := fmt.Sprintf("%s@secondcontext.local", userExternalID)
	user, err := db.NewUserRepository(pool).Ensure(ctx, db.EnsureUserParams{
		ExternalID:  userExternalID,
		Email:       userEmail,
		DisplayName: userDisplayName,
	})
	if err != nil {
		return fmt.Errorf("ensure demo user: %w", err)
	}

	baseURL, cleanup, serverMode, err := resolveBaseURL(cfg, pool)
	if err != nil {
		return err
	}
	defer cleanup()

	client := &demoClient{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}

	printSection("End-to-End Demo")
	printItem("Scenario", scenario.ScenarioName)
	printItem("User", fmt.Sprintf("%s (%s)", user.DisplayName, user.ExternalID))
	printItem("Server", serverMode)
	printItem("Base URL", baseURL)

	seedState, err := seedScenarioState(ctx, pool, client, scenario, user.ID, user.ExternalID)
	if err != nil {
		return err
	}

	printSection("Seeded Memories")
	for _, item := range scenario.Memories {
		memory := seedState.Memories[item.Key]
		fmt.Printf("- %s: %s\n", item.Key, memory.Summary)
	}

	printSection("Seeded Models")
	for _, item := range scenario.PeopleModels {
		fmt.Printf("- %s on %s: readiness %.2f, competence %.2f, capacity %.2f, confidence %.2f\n", item.PersonName, item.TopicName, item.Readiness, item.Competence, item.Capacity, item.Confidence)
	}

	printSection("Seeded Beliefs")
	for _, item := range scenario.Beliefs {
		fmt.Printf("- [%s] %s (%s %.2f)\n", item.TopicName, item.Claim, item.Stance, item.Confidence)
	}

	baselineSessionID := fmt.Sprintf("demo-baseline-%s", runID)
	baselineResponse, err := client.createResponse(ctx, createResponseRequest{
		Model:         demoModel,
		Input:         scenario.Query,
		DisableMemory: true,
		User:          user.ExternalID,
		Metadata: map[string]any{
			"session_id":       baselineSessionID,
			"user_external_id": user.ExternalID,
			"goal":             scenario.Goal,
			"people":           scenario.People,
			"topics":           scenario.Topics,
			"memory_mode":      "social_strategy",
		},
	})
	if err != nil {
		return fmt.Errorf("request baseline response: %w", err)
	}

	printSection("Stateless Draft")
	printParagraph(baselineResponse.OutputText)

	demoSessionID := fmt.Sprintf("demo-run-%s", runID)
	augmentedResponse, err := client.createResponse(ctx, createResponseRequest{
		Model: demoModel,
		Input: scenario.Query,
		User:  user.ExternalID,
		Metadata: map[string]any{
			"session_id":       demoSessionID,
			"user_external_id": user.ExternalID,
			"goal":             scenario.Goal,
			"people":           scenario.People,
			"topics":           scenario.Topics,
			"memory_mode":      "social_strategy",
		},
	})
	if err != nil {
		return fmt.Errorf("request memory-augmented response: %w", err)
	}

	printSection("Memory-Augmented Draft")
	printParagraph(augmentedResponse.OutputText)

	searchResults, err := client.searchMemories(ctx, memorySearchRequest{
		Query:          scenario.Query,
		Goal:           scenario.Goal,
		UserExternalID: user.ExternalID,
		People:         scenario.People,
		Topics:         scenario.Topics,
		Limit:          5,
	})
	if err != nil {
		return fmt.Errorf("search demo memories: %w", err)
	}

	printSection("Retrieved Memories")
	for _, result := range searchResults.Data {
		fmt.Printf("%d. %s [type=%s final=%.2f retrieval=%.2f goal=%.2f]\n", result.Rank, result.Memory.Summary, result.Memory.MemoryType, result.Scores.Final, result.Scores.Retrieval, result.Scores.GoalRelevance)
	}

	scenarioResponse, err := client.createResponse(ctx, createResponseRequest{
		Model:        demoModel,
		Input:        scenario.Query,
		Instructions: scenario.StrategyInstructions,
		User:         user.ExternalID,
		Metadata: map[string]any{
			"session_id":       demoSessionID,
			"user_external_id": user.ExternalID,
			"goal":             scenario.Goal,
			"people":           scenario.People,
			"topics":           scenario.Topics,
			"memory_mode":      "scenario_generation",
		},
	})
	if err != nil {
		return fmt.Errorf("request scenario generation: %w", err)
	}

	plan, _ := parseScenarioPlanFromMetadata(scenarioResponse.Metadata)
	printSection("Scenario Recommendation")
	if plan != nil {
		recommended := plan.recommended()
		fmt.Printf("- Recommended: %s (%.0f%%)\n", recommended.Label, recommended.LikelihoodOfSuccess*100)
		fmt.Printf("- Why: %s\n", collapseWhitespace(plan.RecommendationRationale))
		fmt.Printf("- Draft: %s\n", collapseWhitespace(recommended.MessageDraft))
		fmt.Printf("- Predicted response: %s\n", collapseWhitespace(recommended.PredictedResponse))
	} else {
		printParagraph(scenarioResponse.OutputText)
	}

	debugBefore, err := client.getDebugContext(ctx, demoSessionID)
	if err != nil {
		return fmt.Errorf("inspect debug context before outcome: %w", err)
	}

	printSection("Debug Context Before Outcome")
	printContextMemories(debugBefore.CurrentContextPacket)
	printModelSummaries(debugBefore.PeopleModels)
	printBeliefSummaries(debugBefore.RelevantBeliefs)

	outcomeResponse, err := client.createOutcome(ctx, createOutcomeRequest{
		SessionID: demoSessionID,
		RawText:   scenario.OutcomeText,
		Goal:      scenario.Goal,
		People:    scenario.People,
		Topics:    scenario.Topics,
		User:      user.ExternalID,
		Metadata: map[string]any{
			"source": demoSourceLabel,
		},
	})
	if err != nil {
		return fmt.Errorf("submit demo outcome: %w", err)
	}

	printSection("Outcome Feedback")
	fmt.Printf("- Summary: %s\n", collapseWhitespace(outcomeResponse.Analysis.Summary))
	fmt.Printf("- Success score: %.2f\n", outcomeResponse.Analysis.SuccessScore)
	if strings.TrimSpace(outcomeResponse.Analysis.PredictionError) != "" {
		fmt.Printf("- Prediction error: %s\n", collapseWhitespace(outcomeResponse.Analysis.PredictionError))
	}
	fmt.Printf("- New memory: %s\n", outcomeResponse.Memory.Summary)

	debugAfter, err := client.getDebugContext(ctx, demoSessionID)
	if err != nil {
		return fmt.Errorf("inspect debug context after outcome: %w", err)
	}

	printSection("Model Update After Outcome")
	printModelSummaries(debugAfter.PeopleModels)
	printBeliefSummaries(debugAfter.RelevantBeliefs)
	if debugAfter.LatestTurnUpdates.Outcome != nil {
		fmt.Printf("- Latest outcome score: %.2f\n", debugAfter.LatestTurnUpdates.Outcome.SuccessScore)
	}
	for _, memory := range debugAfter.LatestTurnUpdates.Memories {
		fmt.Printf("- Latest-turn memory: %s\n", memory.Summary)
	}

	followUpSessionID := fmt.Sprintf("demo-follow-up-%s", runID)
	followUpResponse, err := client.createResponse(ctx, createResponseRequest{
		Model: demoModel,
		Input: scenario.FollowUpQuery,
		User:  user.ExternalID,
		Metadata: map[string]any{
			"session_id":       followUpSessionID,
			"user_external_id": user.ExternalID,
			"goal":             scenario.Goal,
			"people":           scenario.People,
			"topics":           scenario.Topics,
			"memory_mode":      "social_strategy",
		},
	})
	if err != nil {
		return fmt.Errorf("request follow-up response: %w", err)
	}

	followUpDebug, err := client.getDebugContext(ctx, followUpSessionID)
	if err != nil {
		return fmt.Errorf("inspect follow-up debug context: %w", err)
	}

	printSection("Follow-Up Draft")
	printParagraph(followUpResponse.OutputText)

	printSection("Changed Behavior Evidence")
	printContextMemories(followUpDebug.CurrentContextPacket)
	foundOutcome := false
	if followUpDebug.CurrentContextPacket != nil {
		for _, memory := range followUpDebug.CurrentContextPacket.MemoryContext {
			if strings.EqualFold(strings.TrimSpace(memory.Type), "outcome") || strings.Contains(strings.ToLower(memory.Summary), "api section") {
				foundOutcome = true
				break
			}
		}
	}
	if foundOutcome {
		fmt.Println("- Follow-up retrieval includes the recorded outcome, so the second answer is using updated interaction evidence rather than only the original seed memories.")
	} else {
		fmt.Println("- Follow-up retrieval completed, but the new outcome memory did not appear in the top retrieved set for this run.")
	}

	printSection("Run Summary")
	printItem("Baseline session", baselineSessionID)
	printItem("Scenario session", demoSessionID)
	printItem("Follow-up session", followUpSessionID)
	printItem("Demo user", user.ExternalID)
	printItem("Hint", "Use the session IDs above with /debug/context if you run against an external dev server.")

	return nil
}

func loadScenario() (demoScenario, error) {
	var scenario demoScenario
	if err := json.Unmarshal(demoSeedJSON, &scenario); err != nil {
		return demoScenario{}, fmt.Errorf("decode demo seed data: %w", err)
	}
	return scenario, nil
}

func resolveBaseURL(cfg config.Config, pool *pgxpool.Pool) (string, func(), string, error) {
	if baseURL := strings.TrimSpace(os.Getenv(demoBaseURLEnv)); baseURL != "" {
		return strings.TrimRight(baseURL, "/"), func() {}, "external API", nil
	}

	embeddedCfg := cfg
	embeddedCfg.App.Env = "development"
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: cfg.Log.Level}))
	server := httptest.NewServer(api.NewServer(embeddedCfg, logger, pool).Handler())
	return server.URL, server.Close, "embedded development server", nil
}

func seedScenarioState(ctx context.Context, pool *pgxpool.Pool, client *demoClient, scenario demoScenario, userID, userExternalID string) (demoSeedState, error) {
	state := demoSeedState{Memories: make(map[string]memoryResponse, len(scenario.Memories))}

	for _, item := range scenario.Memories {
		memory, err := client.ingestMemory(ctx, ingestMemoryRequest{
			RawText:      item.RawText,
			Summary:      item.Summary,
			Type:         item.Type,
			Source:       firstNonEmpty(item.Source, demoSourceLabel),
			People:       item.People,
			Topics:       item.Topics,
			Importance:   item.Importance,
			Utility:      item.Utility,
			BeliefImpact: item.BeliefImpact,
			Confidence:   item.Confidence,
			User:         userExternalID,
			Metadata: map[string]any{
				"source":           demoSourceLabel,
				"demo_memory_key":  item.Key,
				"user_external_id": userExternalID,
			},
		})
		if err != nil {
			return demoSeedState{}, fmt.Errorf("seed memory %s: %w", item.Key, err)
		}
		state.Memories[item.Key] = memory
	}

	peopleRepo := db.NewPersonRepository(pool)
	topicRepo := db.NewTopicRepository(pool)
	modelRepo := db.NewPersonTopicModelRepository(pool)
	beliefRepo := db.NewBeliefRepository(pool)

	for _, item := range scenario.PeopleModels {
		person, err := peopleRepo.Upsert(ctx, db.UpsertPersonParams{
			UserID:   userID,
			Name:     item.PersonName,
			Aliases:  item.PersonAliases,
			Metadata: mustJSON(map[string]any{"source": demoSourceLabel}),
		})
		if err != nil {
			return demoSeedState{}, fmt.Errorf("seed person %s: %w", item.PersonName, err)
		}
		topic, err := topicRepo.Upsert(ctx, db.UpsertTopicParams{
			UserID:   userID,
			Name:     item.TopicName,
			Aliases:  item.TopicAliases,
			Metadata: mustJSON(map[string]any{"source": demoSourceLabel}),
		})
		if err != nil {
			return demoSeedState{}, fmt.Errorf("seed topic %s: %w", item.TopicName, err)
		}
		evidenceIDs, err := resolveMemoryIDs(state.Memories, item.EvidenceMemoryKeys)
		if err != nil {
			return demoSeedState{}, fmt.Errorf("resolve model evidence for %s on %s: %w", item.PersonName, item.TopicName, err)
		}
		if _, err := modelRepo.Save(ctx, db.SavePersonTopicModelParams{
			UserID:         userID,
			PersonID:       person.ID,
			TopicID:        topic.ID,
			Niceness:       item.Niceness,
			Readiness:      item.Readiness,
			Competence:     item.Competence,
			Capacity:       item.Capacity,
			Confidence:     item.Confidence,
			EvidenceCount:  len(evidenceIDs),
			LastObservedAt: timePointer(time.Now().UTC()),
			Metadata: mustJSON(map[string]any{
				"source":               demoSourceLabel,
				"evidence_memory_ids":  evidenceIDs,
				"evidence_memory_keys": item.EvidenceMemoryKeys,
			}),
		}); err != nil {
			return demoSeedState{}, fmt.Errorf("seed person/topic model for %s on %s: %w", item.PersonName, item.TopicName, err)
		}
	}

	for _, item := range scenario.Beliefs {
		topic, err := topicRepo.Upsert(ctx, db.UpsertTopicParams{
			UserID:   userID,
			Name:     item.TopicName,
			Aliases:  item.TopicAliases,
			Metadata: mustJSON(map[string]any{"source": demoSourceLabel}),
		})
		if err != nil {
			return demoSeedState{}, fmt.Errorf("seed belief topic %s: %w", item.TopicName, err)
		}
		evidenceIDs, err := resolveMemoryIDs(state.Memories, item.EvidenceMemoryKeys)
		if err != nil {
			return demoSeedState{}, fmt.Errorf("resolve belief evidence for %s: %w", item.Claim, err)
		}
		if _, err := beliefRepo.Save(ctx, db.SaveBeliefParams{
			UserID:            userID,
			TopicID:           topic.ID,
			Claim:             item.Claim,
			Stance:            item.Stance,
			Confidence:        item.Confidence,
			EvidenceMemoryIDs: evidenceIDs,
			LastUpdatedAt:     time.Now().UTC(),
			Metadata: mustJSON(map[string]any{
				"source":               demoSourceLabel,
				"evidence_memory_ids":  evidenceIDs,
				"evidence_memory_keys": item.EvidenceMemoryKeys,
			}),
		}); err != nil {
			return demoSeedState{}, fmt.Errorf("seed belief %s: %w", item.Claim, err)
		}
	}

	return state, nil
}

func resolveMemoryIDs(memories map[string]memoryResponse, keys []string) ([]string, error) {
	ids := make([]string, 0, len(keys))
	for _, key := range keys {
		memory, ok := memories[key]
		if !ok {
			return nil, fmt.Errorf("unknown memory key %q", key)
		}
		ids = append(ids, memory.ID)
	}
	return ids, nil
}

func parseScenarioPlanFromMetadata(metadata map[string]json.RawMessage) (*scenarioPlan, error) {
	raw, ok := metadata["scenario_plan"]
	if !ok || len(raw) == 0 {
		return nil, nil
	}
	var plan scenarioPlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func (p *scenarioPlan) recommended() scenarioStrategy {
	if p == nil || len(p.Strategies) == 0 {
		return scenarioStrategy{}
	}
	for _, strategy := range p.Strategies {
		if strategy.ID == p.RecommendedStrategyID {
			return strategy
		}
	}
	return p.Strategies[0]
}

func (c *demoClient) ingestMemory(ctx context.Context, request ingestMemoryRequest) (memoryResponse, error) {
	var response memoryResponse
	err := c.doJSON(ctx, http.MethodPost, "/memory/ingest", request, &response)
	return response, err
}

func (c *demoClient) createResponse(ctx context.Context, request createResponseRequest) (createResponseResult, error) {
	var response createResponseResult
	err := c.doJSON(ctx, http.MethodPost, "/v1/responses", request, &response)
	return response, err
}

func (c *demoClient) searchMemories(ctx context.Context, request memorySearchRequest) (memorySearchResponse, error) {
	var response memorySearchResponse
	err := c.doJSON(ctx, http.MethodPost, "/memory/search", request, &response)
	return response, err
}

func (c *demoClient) createOutcome(ctx context.Context, request createOutcomeRequest) (createOutcomeResponse, error) {
	var response createOutcomeResponse
	err := c.doJSON(ctx, http.MethodPost, "/interactions/outcome", request, &response)
	return response, err
}

func (c *demoClient) getDebugContext(ctx context.Context, sessionID string) (debugContextResponse, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/debug/context?session_id="+sessionID, nil)
	if err != nil {
		return debugContextResponse{}, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return debugContextResponse{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		return debugContextResponse{}, &demoHTTPError{StatusCode: response.StatusCode, Body: string(body)}
	}
	var payload debugContextResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return debugContextResponse{}, err
	}
	return payload, nil
}

func (c *demoClient) doJSON(ctx context.Context, method, path string, requestBody, responseBody any) error {
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(response.Body)
		return &demoHTTPError{StatusCode: response.StatusCode, Body: string(body)}
	}
	return json.NewDecoder(response.Body).Decode(responseBody)
}

func printSection(title string) {
	fmt.Printf("\n== %s ==\n", title)
}

func printItem(label, value string) {
	fmt.Printf("- %s: %s\n", label, value)
}

func printParagraph(value string) {
	fmt.Println(collapseWhitespace(value))
}

func printContextMemories(packet *contextPacket) {
	if packet == nil || len(packet.MemoryContext) == 0 {
		fmt.Println("- No retrieved memories in context.")
		return
	}
	for index, memory := range packet.MemoryContext {
		fmt.Printf("%d. %s [type=%s final=%.2f retrieval=%.2f goal=%.2f]\n", index+1, collapseWhitespace(memory.Summary), memory.Type, memory.Scores.Final, memory.Scores.Retrieval, memory.Scores.GoalRelevance)
	}
	for _, line := range packet.PeopleContext {
		fmt.Printf("- People context: %s\n", collapseWhitespace(line))
	}
	for _, line := range packet.BeliefContext {
		fmt.Printf("- Belief context: %s\n", collapseWhitespace(line))
	}
}

func printModelSummaries(models []personDebugResponse) {
	if len(models) == 0 {
		fmt.Println("- No person/topic models returned.")
		return
	}
	for _, person := range models {
		for _, model := range person.Models {
			fmt.Printf("- %s on %s: %s [readiness=%.2f competence=%.2f capacity=%.2f confidence=%.2f]\n", person.Name, model.TopicName, collapseWhitespace(model.Summary), model.Readiness, model.Competence, model.Capacity, model.Confidence)
		}
	}
}

func printBeliefSummaries(beliefs []beliefDebugResponse) {
	if len(beliefs) == 0 {
		fmt.Println("- No relevant beliefs returned.")
		return
	}
	for _, belief := range beliefs {
		fmt.Printf("- [%s] %s (%s %.2f)\n", belief.TopicName, collapseWhitespace(belief.Claim), belief.Stance, belief.Confidence)
	}
}

func collapseWhitespace(value string) string {
	parts := strings.Fields(strings.TrimSpace(value))
	return strings.Join(parts, " ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func mustJSON(value any) json.RawMessage {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return payload
}

func timePointer(value time.Time) *time.Time {
	return &value
}
