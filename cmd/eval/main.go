package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bdobrica/SecondContext/internal/api"
	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/llm"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	evalBaseURLEnv     = "SECOND_CONTEXT_BASE_URL"
	evalOutputDirEnv   = "EVAL_OUTPUT_DIR"
	evalOutputDir      = ".artifacts/evaluation"
	evalSourceLabel    = "eval.evaluation"
	evalCollectionPref = "evaluation"
	evalModel          = "context-agent-1"
	judgeResponseModel = "gpt-4.1-mini"
)

//go:embed dataset.json
var evaluationDatasetJSON []byte

type evaluationDataset struct {
	Name  string           `json:"name"`
	Cases []evaluationCase `json:"cases"`
}

type evaluationCase struct {
	ID                        string            `json:"id"`
	Title                     string            `json:"title"`
	Query                     string            `json:"query"`
	Goal                      string            `json:"goal"`
	People                    []string          `json:"people"`
	Topics                    []string          `json:"topics"`
	Instructions              string            `json:"instructions"`
	StrategyInstructions      string            `json:"strategy_instructions"`
	OutcomeText               string            `json:"outcome_text"`
	FollowUpQuery             string            `json:"follow_up_query"`
	ExpectedMemoryKeys        []string          `json:"expected_memory_keys"`
	ExpectedAugmentedCues     []string          `json:"expected_augmented_cues"`
	ExpectedFollowUpCues      []string          `json:"expected_follow_up_cues"`
	Memories                  []seedMemory      `json:"memories"`
	PeopleModels              []seedPersonModel `json:"people_models"`
	Beliefs                   []seedBelief      `json:"beliefs"`
	ManualRatingInstructions  string            `json:"manual_rating_instructions"`
	ManualPrecisionAssessment string            `json:"manual_precision_assessment"`
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
	ID         string         `json:"id"`
	MemoryType string         `json:"type"`
	Summary    string         `json:"summary"`
	People     []string       `json:"people"`
	Topics     []string       `json:"topics"`
	Confidence float64        `json:"confidence"`
	Metadata   map[string]any `json:"metadata,omitempty"`
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
	TopicName  string  `json:"topic_name"`
	Readiness  float64 `json:"readiness"`
	Competence float64 `json:"competence"`
	Capacity   float64 `json:"capacity"`
	Confidence float64 `json:"confidence"`
	Summary    string  `json:"summary"`
}

type beliefDebugResponse struct {
	TopicName  string  `json:"topic_name"`
	Claim      string  `json:"claim"`
	Stance     string  `json:"stance"`
	Confidence float64 `json:"confidence"`
	Summary    string  `json:"summary"`
}

type debugLatestTurnResponse struct {
	Outcome  *outcomeResponse `json:"outcome,omitempty"`
	Memories []memoryResponse `json:"memories,omitempty"`
}

type demoClient struct {
	baseURL    string
	httpClient *http.Client
}

type seedState struct {
	Memories map[string]memoryResponse
}

type evaluationReport struct {
	GeneratedAt time.Time           `json:"generated_at"`
	DatasetName string              `json:"dataset_name"`
	BaseURL     string              `json:"base_url"`
	ServerMode  string              `json:"server_mode"`
	Cases       []evaluationCaseRun `json:"cases"`
	Summary     evaluationSummary   `json:"summary"`
	ManualGuide manualRatingGuide   `json:"manual_guide"`
	JudgeModel  string              `json:"judge_model"`
}

type evaluationCaseRun struct {
	ID                   string                 `json:"id"`
	Title                string                 `json:"title"`
	UserExternalID       string                 `json:"user_external_id"`
	BaselineSessionID    string                 `json:"baseline_session_id"`
	AugmentedSessionID   string                 `json:"augmented_session_id"`
	FollowUpSessionID    string                 `json:"follow_up_session_id,omitempty"`
	Baseline             responseMetrics        `json:"baseline"`
	Augmented            responseMetrics        `json:"augmented"`
	Retrieval            retrievalMetrics       `json:"retrieval"`
	Strategy             *strategyMetrics       `json:"strategy,omitempty"`
	Outcome              *outcomeMetrics        `json:"outcome,omitempty"`
	FollowUp             *followUpMetrics       `json:"follow_up,omitempty"`
	Judge                *judgeComparison       `json:"judge,omitempty"`
	Manual               manualRatings          `json:"manual"`
	DebugContextMemories []debugMemorySelection `json:"debug_context_memories,omitempty"`
	Error                string                 `json:"error,omitempty"`
}

type responseMetrics struct {
	OutputText     string   `json:"output_text"`
	CueHits        int      `json:"cue_hits"`
	CueMisses      []string `json:"cue_misses,omitempty"`
	OutputWords    int      `json:"output_words"`
	PeopleMentions []string `json:"people_mentions,omitempty"`
	TopicMentions  []string `json:"topic_mentions,omitempty"`
}

type retrievalMetrics struct {
	ExpectedMemoryKeys  []string               `json:"expected_memory_keys"`
	RetrievedMemoryKeys []string               `json:"retrieved_memory_keys"`
	TopResults          []retrievedMemoryBrief `json:"top_results"`
	Hits                int                    `json:"hits"`
	PrecisionAt5        float64                `json:"precision_at_5"`
	Coverage            float64                `json:"coverage"`
	ManualAssessment    string                 `json:"manual_assessment"`
}

type retrievedMemoryBrief struct {
	Key       string  `json:"key,omitempty"`
	Summary   string  `json:"summary"`
	Type      string  `json:"type"`
	Final     float64 `json:"final"`
	Retrieval float64 `json:"retrieval"`
	Goal      float64 `json:"goal_relevance"`
}

type strategyMetrics struct {
	RecommendedLabel         string  `json:"recommended_label,omitempty"`
	LikelihoodOfSuccess      float64 `json:"likelihood_of_success,omitempty"`
	RecommendationRationale  string  `json:"recommendation_rationale,omitempty"`
	StrategyCount            int     `json:"strategy_count"`
	PredictedResponseSnippet string  `json:"predicted_response_snippet,omitempty"`
}

type outcomeMetrics struct {
	Summary                   string  `json:"summary"`
	SuccessScore              float64 `json:"success_score"`
	PredictionError           string  `json:"prediction_error,omitempty"`
	PredictionErrorAbsolute   float64 `json:"prediction_error_absolute,omitempty"`
	UsefulMemoriesCreated     int     `json:"useful_memories_created"`
	CreatedOutcomeMemory      string  `json:"created_outcome_memory,omitempty"`
	BehaviorChangeSupported   bool    `json:"behavior_change_supported"`
	BehaviorChangeSupportNote string  `json:"behavior_change_support_note,omitempty"`
}

type followUpMetrics struct {
	OutputText            string   `json:"output_text"`
	CueHits               int      `json:"cue_hits"`
	CueMisses             []string `json:"cue_misses,omitempty"`
	OutcomeMemoryPresent  bool     `json:"outcome_memory_present"`
	ChangedBehavior       bool     `json:"changed_behavior"`
	ChangedBehaviorReason string   `json:"changed_behavior_reason,omitempty"`
}

type debugMemorySelection struct {
	Summary string  `json:"summary"`
	Type    string  `json:"type"`
	Final   float64 `json:"final"`
	Goal    float64 `json:"goal_relevance"`
}

type judgeComparison struct {
	Relevance       judgeDimension `json:"relevance"`
	Usefulness      judgeDimension `json:"usefulness"`
	Personalization judgeDimension `json:"personalization"`
	StrategyQuality judgeDimension `json:"strategy_quality"`
	OverallWinner   string         `json:"overall_winner"`
	Summary         string         `json:"summary"`
	ParseError      string         `json:"parse_error,omitempty"`
}

type judgeDimension struct {
	BaselineScore  int    `json:"baseline_score"`
	AugmentedScore int    `json:"augmented_score"`
	Winner         string `json:"winner"`
	Reason         string `json:"reason"`
}

type evaluationSummary struct {
	CaseCount                       int            `json:"case_count"`
	FailedCases                     int            `json:"failed_cases"`
	AverageRetrievalPrecisionAt5    float64        `json:"average_retrieval_precision_at_5"`
	AverageRetrievalCoverage        float64        `json:"average_retrieval_coverage"`
	AverageStrategySuccessEstimate  float64        `json:"average_strategy_success_estimate"`
	AverageActualSuccessScore       float64        `json:"average_actual_success_score"`
	AveragePredictionErrorAbsolute  float64        `json:"average_prediction_error_absolute"`
	AverageUsefulMemoriesPerOutcome float64        `json:"average_useful_memories_per_outcome"`
	OutcomeFeedbackChangedCases     int            `json:"outcome_feedback_changed_cases"`
	OutcomeFeedbackEvaluatedCases   int            `json:"outcome_feedback_evaluated_cases"`
	DimensionAugmentedWins          map[string]int `json:"dimension_augmented_wins"`
	DimensionBaselineWins           map[string]int `json:"dimension_baseline_wins"`
	DimensionTies                   map[string]int `json:"dimension_ties"`
	ManualFollowUps                 []string       `json:"manual_follow_ups"`
}

type manualRatings struct {
	UserRatedUsefulness                *int   `json:"user_rated_usefulness,omitempty"`
	UserRatedContextualAppropriateness *int   `json:"user_rated_contextual_appropriateness,omitempty"`
	ReviewerNotes                      string `json:"reviewer_notes,omitempty"`
	Instructions                       string `json:"instructions,omitempty"`
}

type manualRatingGuide struct {
	Scale                  string   `json:"scale"`
	SuggestedQuestions     []string `json:"suggested_questions"`
	PredictionAccuracyNote string   `json:"prediction_accuracy_note"`
	PrecisionReviewNote    string   `json:"precision_review_note"`
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
		return fmt.Errorf("POSTGRES_ENABLED must be true for evaluation")
	}

	dataset, err := loadDataset()
	if err != nil {
		return err
	}

	pool, err := db.Open(ctx, cfg.Postgres)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer db.Close(pool)

	baseURL, cleanup, serverMode, err := resolveBaseURL(cfg, pool)
	if err != nil {
		return err
	}
	defer cleanup()

	client := &demoClient{baseURL: baseURL, httpClient: &http.Client{Timeout: 60 * time.Second}}
	judgeClient := llm.NewOpenAIClient(cfg.OpenAI)
	runID := time.Now().UTC().Format("20060102t150405")
	if strings.TrimSpace(os.Getenv(evalBaseURLEnv)) == "" {
		cfg.Qdrant.Collection = fmt.Sprintf("%s_%s", evalCollectionPref, runID)
	}

	report := evaluationReport{
		GeneratedAt: time.Now().UTC(),
		DatasetName: dataset.Name,
		BaseURL:     baseURL,
		ServerMode:  serverMode,
		Cases:       make([]evaluationCaseRun, 0, len(dataset.Cases)),
		JudgeModel:  judgeResponseModel,
		ManualGuide: manualRatingGuide{
			Scale: "Use a 1-5 scale where 1 is poor and 5 is excellent.",
			SuggestedQuestions: []string{
				"Did the memory-augmented answer feel more useful than the stateless answer?",
				"Did the augmented answer use the right person/topic context without sounding fabricated?",
				"Were the retrieved memories actually the most relevant ones for the task?",
			},
			PredictionAccuracyNote: "Compare strategy likelihood estimates with actual success scores on cases that include outcome feedback.",
			PrecisionReviewNote:    "Review whether the expected memory keys are truly the right gold set before locking in precision claims.",
		},
	}

	for _, currentCase := range dataset.Cases {
		caseRun, caseErr := evaluateCase(ctx, cfg, pool, client, judgeClient, runID, currentCase)
		if caseErr != nil {
			caseRun.Error = caseErr.Error()
		}
		report.Cases = append(report.Cases, caseRun)
	}

	report.Summary = summarizeReport(report.Cases)
	outputDir := strings.TrimSpace(os.Getenv(evalOutputDirEnv))
	if outputDir == "" {
		outputDir = evalOutputDir
	}
	jsonPath, markdownPath, err := writeReportFiles(report, outputDir)
	if err != nil {
		return err
	}

	printSummary(report, jsonPath, markdownPath)
	return nil
}

func evaluateCase(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, client *demoClient, judgeClient llm.Client, runID string, currentCase evaluationCase) (evaluationCaseRun, error) {
	caseRun := evaluationCaseRun{ID: currentCase.ID, Title: currentCase.Title}
	userExternalID := fmt.Sprintf("eval-%s-%s", normalizeID(currentCase.ID), runID)
	userDisplayName := strings.TrimSpace(currentCase.Title)
	if userDisplayName == "" {
		userDisplayName = currentCase.ID
	}
	user, err := db.NewUserRepository(pool).Ensure(ctx, db.EnsureUserParams{
		ExternalID:  userExternalID,
		Email:       fmt.Sprintf("%s@secondcontext.local", userExternalID),
		DisplayName: userDisplayName,
	})
	if err != nil {
		return caseRun, fmt.Errorf("ensure evaluation user: %w", err)
	}
	caseRun.UserExternalID = user.ExternalID
	caseRun.Manual = manualRatings{Instructions: currentCase.ManualRatingInstructions}

	seed, err := seedEvaluationState(ctx, pool, client, currentCase, user.ID, user.ExternalID)
	if err != nil {
		return caseRun, err
	}

	baselineSessionID := fmt.Sprintf("eval-%s-baseline-%s", normalizeID(currentCase.ID), runID)
	augmentedSessionID := fmt.Sprintf("eval-%s-augmented-%s", normalizeID(currentCase.ID), runID)
	caseRun.BaselineSessionID = baselineSessionID
	caseRun.AugmentedSessionID = augmentedSessionID

	baselineResponse, err := client.createResponse(ctx, buildResponseRequest(currentCase, user.ExternalID, baselineSessionID, true))
	if err != nil {
		return caseRun, fmt.Errorf("baseline response: %w", err)
	}
	caseRun.Baseline = buildResponseMetrics(baselineResponse.OutputText, currentCase.ExpectedAugmentedCues, currentCase.People, currentCase.Topics)

	augmentedResponse, err := client.createResponse(ctx, buildResponseRequest(currentCase, user.ExternalID, augmentedSessionID, false))
	if err != nil {
		return caseRun, fmt.Errorf("memory-augmented response: %w", err)
	}
	caseRun.Augmented = buildResponseMetrics(augmentedResponse.OutputText, currentCase.ExpectedAugmentedCues, currentCase.People, currentCase.Topics)

	searchResults, err := client.searchMemories(ctx, memorySearchRequest{
		Query:          currentCase.Query,
		Goal:           currentCase.Goal,
		UserExternalID: user.ExternalID,
		People:         currentCase.People,
		Topics:         currentCase.Topics,
		Limit:          5,
	})
	if err != nil {
		return caseRun, fmt.Errorf("memory search: %w", err)
	}
	caseRun.Retrieval = buildRetrievalMetrics(currentCase, searchResults, seed)

	debugResponse, err := client.getDebugContext(ctx, augmentedSessionID)
	if err != nil {
		return caseRun, fmt.Errorf("debug context: %w", err)
	}
	caseRun.DebugContextMemories = extractDebugMemories(debugResponse)

	if strings.TrimSpace(currentCase.StrategyInstructions) != "" {
		strategyResponse, strategyErr := client.createResponse(ctx, buildStrategyRequest(currentCase, user.ExternalID, augmentedSessionID))
		if strategyErr != nil {
			return caseRun, fmt.Errorf("strategy generation: %w", strategyErr)
		}
		plan, _ := parseScenarioPlanFromMetadata(strategyResponse.Metadata)
		caseRun.Strategy = buildStrategyMetrics(plan)
	}

	judge, judgeErr := judgeResponses(ctx, cfg, judgeClient, currentCase, baselineResponse.OutputText, augmentedResponse.OutputText)
	if judgeErr == nil {
		caseRun.Judge = judge
	} else {
		caseRun.Judge = &judgeComparison{ParseError: judgeErr.Error()}
	}

	if strings.TrimSpace(currentCase.OutcomeText) != "" {
		outcomeResponse, outcomeErr := client.createOutcome(ctx, createOutcomeRequest{
			SessionID: augmentedSessionID,
			RawText:   currentCase.OutcomeText,
			Goal:      currentCase.Goal,
			People:    currentCase.People,
			Topics:    currentCase.Topics,
			User:      user.ExternalID,
			Metadata:  map[string]any{"source": evalSourceLabel, "evaluation_case_id": currentCase.ID},
		})
		if outcomeErr != nil {
			return caseRun, fmt.Errorf("outcome submission: %w", outcomeErr)
		}

		debugAfter, debugErr := client.getDebugContext(ctx, augmentedSessionID)
		if debugErr != nil {
			return caseRun, fmt.Errorf("debug context after outcome: %w", debugErr)
		}

		predictionErrorAbsolute := 0.0
		if caseRun.Strategy != nil && caseRun.Strategy.LikelihoodOfSuccess > 0 {
			predictionErrorAbsolute = math.Abs(caseRun.Strategy.LikelihoodOfSuccess - outcomeResponse.Analysis.SuccessScore)
		}
		caseRun.Outcome = &outcomeMetrics{
			Summary:                 collapseWhitespace(outcomeResponse.Analysis.Summary),
			SuccessScore:            outcomeResponse.Analysis.SuccessScore,
			PredictionError:         collapseWhitespace(outcomeResponse.Analysis.PredictionError),
			PredictionErrorAbsolute: predictionErrorAbsolute,
			UsefulMemoriesCreated:   len(debugAfter.LatestTurnUpdates.Memories),
			CreatedOutcomeMemory:    collapseWhitespace(outcomeResponse.Memory.Summary),
		}

		if strings.TrimSpace(currentCase.FollowUpQuery) != "" {
			followUpSessionID := fmt.Sprintf("eval-%s-followup-%s", normalizeID(currentCase.ID), runID)
			caseRun.FollowUpSessionID = followUpSessionID
			followUpResponse, followErr := client.createResponse(ctx, createResponseRequest{
				Model:        evalModel,
				Input:        currentCase.FollowUpQuery,
				Instructions: currentCase.Instructions,
				User:         user.ExternalID,
				Metadata: map[string]any{
					"session_id":       followUpSessionID,
					"user_external_id": user.ExternalID,
					"goal":             currentCase.Goal,
					"people":           currentCase.People,
					"topics":           currentCase.Topics,
					"memory_mode":      "social_strategy",
				},
			})
			if followErr != nil {
				return caseRun, fmt.Errorf("follow-up response: %w", followErr)
			}
			followDebug, followDebugErr := client.getDebugContext(ctx, followUpSessionID)
			if followDebugErr != nil {
				return caseRun, fmt.Errorf("follow-up debug context: %w", followDebugErr)
			}

			followHits, followMisses := cueHits(followUpResponse.OutputText, currentCase.ExpectedFollowUpCues)
			outcomeMemoryPresent := followUpHasOutcomeMemory(followDebug)
			changedBehavior := outcomeMemoryPresent || followHits > 0
			reason := "follow-up did not surface the expected changed-behavior cues"
			if changedBehavior {
				reason = "follow-up retrieval included the new outcome evidence or the response adopted the expected post-outcome cues"
			}
			caseRun.FollowUp = &followUpMetrics{
				OutputText:            collapseWhitespace(followUpResponse.OutputText),
				CueHits:               followHits,
				CueMisses:             followMisses,
				OutcomeMemoryPresent:  outcomeMemoryPresent,
				ChangedBehavior:       changedBehavior,
				ChangedBehaviorReason: reason,
			}
			caseRun.Outcome.BehaviorChangeSupported = changedBehavior
			caseRun.Outcome.BehaviorChangeSupportNote = reason
		}
	}

	return caseRun, nil
}

func loadDataset() (evaluationDataset, error) {
	var dataset evaluationDataset
	if err := json.Unmarshal(evaluationDatasetJSON, &dataset); err != nil {
		return evaluationDataset{}, fmt.Errorf("decode evaluation dataset: %w", err)
	}
	return dataset, nil
}

func resolveBaseURL(cfg config.Config, pool *pgxpool.Pool) (string, func(), string, error) {
	if baseURL := strings.TrimSpace(os.Getenv(evalBaseURLEnv)); baseURL != "" {
		return strings.TrimRight(baseURL, "/"), func() {}, "external API", nil
	}

	embeddedCfg := cfg
	embeddedCfg.App.Env = "development"
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: cfg.Log.Level}))
	server := httptest.NewServer(api.NewServer(embeddedCfg, logger, pool).Handler())
	return server.URL, server.Close, "embedded development server", nil
}

func buildResponseRequest(currentCase evaluationCase, userExternalID, sessionID string, disableMemory bool) createResponseRequest {
	return createResponseRequest{
		Model:         evalModel,
		Input:         currentCase.Query,
		Instructions:  currentCase.Instructions,
		DisableMemory: disableMemory,
		User:          userExternalID,
		Metadata: map[string]any{
			"session_id":       sessionID,
			"user_external_id": userExternalID,
			"goal":             currentCase.Goal,
			"people":           currentCase.People,
			"topics":           currentCase.Topics,
			"memory_mode":      "social_strategy",
		},
	}
}

func buildStrategyRequest(currentCase evaluationCase, userExternalID, sessionID string) createResponseRequest {
	return createResponseRequest{
		Model:        evalModel,
		Input:        currentCase.Query,
		Instructions: currentCase.StrategyInstructions,
		User:         userExternalID,
		Metadata: map[string]any{
			"session_id":       sessionID,
			"user_external_id": userExternalID,
			"goal":             currentCase.Goal,
			"people":           currentCase.People,
			"topics":           currentCase.Topics,
			"memory_mode":      "scenario_generation",
		},
	}
}

func seedEvaluationState(ctx context.Context, pool *pgxpool.Pool, client *demoClient, currentCase evaluationCase, userID, userExternalID string) (seedState, error) {
	state := seedState{Memories: make(map[string]memoryResponse, len(currentCase.Memories))}

	for _, item := range currentCase.Memories {
		memory, err := client.ingestMemory(ctx, ingestMemoryRequest{
			RawText:      item.RawText,
			Summary:      item.Summary,
			Type:         item.Type,
			Source:       firstNonEmpty(item.Source, evalSourceLabel),
			People:       item.People,
			Topics:       item.Topics,
			Importance:   item.Importance,
			Utility:      item.Utility,
			BeliefImpact: item.BeliefImpact,
			Confidence:   item.Confidence,
			User:         userExternalID,
			Metadata: map[string]any{
				"source":                evalSourceLabel,
				"evaluation_case_id":    currentCase.ID,
				"evaluation_memory_key": item.Key,
				"user_external_id":      userExternalID,
			},
		})
		if err != nil {
			return seedState{}, fmt.Errorf("seed memory %s: %w", item.Key, err)
		}
		state.Memories[item.Key] = memory
	}

	peopleRepo := db.NewPersonRepository(pool)
	topicRepo := db.NewTopicRepository(pool)
	modelRepo := db.NewPersonTopicModelRepository(pool)
	beliefRepo := db.NewBeliefRepository(pool)

	for _, item := range currentCase.PeopleModels {
		person, err := peopleRepo.Upsert(ctx, db.UpsertPersonParams{UserID: userID, Name: item.PersonName, Aliases: item.PersonAliases, Metadata: mustJSON(map[string]any{"source": evalSourceLabel})})
		if err != nil {
			return seedState{}, fmt.Errorf("seed person %s: %w", item.PersonName, err)
		}
		topic, err := topicRepo.Upsert(ctx, db.UpsertTopicParams{UserID: userID, Name: item.TopicName, Aliases: item.TopicAliases, Metadata: mustJSON(map[string]any{"source": evalSourceLabel})})
		if err != nil {
			return seedState{}, fmt.Errorf("seed topic %s: %w", item.TopicName, err)
		}
		evidenceIDs, err := resolveMemoryIDs(state.Memories, item.EvidenceMemoryKeys)
		if err != nil {
			return seedState{}, fmt.Errorf("resolve model evidence for %s on %s: %w", item.PersonName, item.TopicName, err)
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
				"source":               evalSourceLabel,
				"evidence_memory_ids":  evidenceIDs,
				"evidence_memory_keys": item.EvidenceMemoryKeys,
			}),
		}); err != nil {
			return seedState{}, fmt.Errorf("seed person/topic model for %s on %s: %w", item.PersonName, item.TopicName, err)
		}
	}

	for _, item := range currentCase.Beliefs {
		topic, err := topicRepo.Upsert(ctx, db.UpsertTopicParams{UserID: userID, Name: item.TopicName, Aliases: item.TopicAliases, Metadata: mustJSON(map[string]any{"source": evalSourceLabel})})
		if err != nil {
			return seedState{}, fmt.Errorf("seed belief topic %s: %w", item.TopicName, err)
		}
		evidenceIDs, err := resolveMemoryIDs(state.Memories, item.EvidenceMemoryKeys)
		if err != nil {
			return seedState{}, fmt.Errorf("resolve belief evidence for %s: %w", item.Claim, err)
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
				"source":               evalSourceLabel,
				"evidence_memory_ids":  evidenceIDs,
				"evidence_memory_keys": item.EvidenceMemoryKeys,
			}),
		}); err != nil {
			return seedState{}, fmt.Errorf("seed belief %s: %w", item.Claim, err)
		}
	}

	return state, nil
}

func buildResponseMetrics(output string, cues, people, topics []string) responseMetrics {
	hits, misses := cueHits(output, cues)
	return responseMetrics{
		OutputText:     collapseWhitespace(output),
		CueHits:        hits,
		CueMisses:      misses,
		OutputWords:    len(strings.Fields(output)),
		PeopleMentions: mentionsInText(output, people),
		TopicMentions:  mentionsInText(output, topics),
	}
}

func buildRetrievalMetrics(currentCase evaluationCase, results memorySearchResponse, seed seedState) retrievalMetrics {
	expectedIDs := make(map[string]string, len(currentCase.ExpectedMemoryKeys))
	for _, key := range currentCase.ExpectedMemoryKeys {
		if memory, ok := seed.Memories[key]; ok {
			expectedIDs[memory.ID] = key
		}
	}

	retrievedKeys := make([]string, 0, len(results.Data))
	topResults := make([]retrievedMemoryBrief, 0, len(results.Data))
	hits := 0
	for _, result := range results.Data {
		key := memoryKey(result.Memory)
		if key != "" {
			retrievedKeys = append(retrievedKeys, key)
		}
		if _, ok := expectedIDs[result.Memory.ID]; ok {
			hits++
		}
		topResults = append(topResults, retrievedMemoryBrief{
			Key:       key,
			Summary:   collapseWhitespace(result.Memory.Summary),
			Type:      result.Memory.MemoryType,
			Final:     result.Scores.Final,
			Retrieval: result.Scores.Retrieval,
			Goal:      result.Scores.GoalRelevance,
		})
	}

	precisionAt5 := 0.0
	if len(results.Data) > 0 {
		precisionAt5 = float64(hits) / 5.0
	}
	coverage := 0.0
	if len(currentCase.ExpectedMemoryKeys) > 0 {
		coverage = float64(hits) / float64(len(currentCase.ExpectedMemoryKeys))
	}

	return retrievalMetrics{
		ExpectedMemoryKeys:  currentCase.ExpectedMemoryKeys,
		RetrievedMemoryKeys: retrievedKeys,
		TopResults:          topResults,
		Hits:                hits,
		PrecisionAt5:        precisionAt5,
		Coverage:            coverage,
		ManualAssessment:    currentCase.ManualPrecisionAssessment,
	}
}

func buildStrategyMetrics(plan *scenarioPlan) *strategyMetrics {
	if plan == nil || len(plan.Strategies) == 0 {
		return nil
	}
	recommended := recommendedStrategy(plan)
	return &strategyMetrics{
		RecommendedLabel:         collapseWhitespace(recommended.Label),
		LikelihoodOfSuccess:      recommended.LikelihoodOfSuccess,
		RecommendationRationale:  collapseWhitespace(plan.RecommendationRationale),
		StrategyCount:            len(plan.Strategies),
		PredictedResponseSnippet: collapseWhitespace(recommended.PredictedResponse),
	}
}

func extractDebugMemories(response debugContextResponse) []debugMemorySelection {
	if response.CurrentContextPacket == nil {
		return nil
	}
	items := make([]debugMemorySelection, 0, len(response.CurrentContextPacket.MemoryContext))
	for _, memory := range response.CurrentContextPacket.MemoryContext {
		items = append(items, debugMemorySelection{
			Summary: collapseWhitespace(memory.Summary),
			Type:    memory.Type,
			Final:   memory.Scores.Final,
			Goal:    memory.Scores.GoalRelevance,
		})
	}
	return items
}

func judgeResponses(ctx context.Context, cfg config.Config, judgeClient llm.Client, currentCase evaluationCase, baseline, augmented string) (*judgeComparison, error) {
	if strings.TrimSpace(cfg.OpenAI.APIKey) == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY is not configured for judge evaluation")
	}

	request := llm.GenerateRequest{
		Model: judgeResponseModel,
		Messages: []llm.Message{{
			Role: "system",
			Content: strings.Join([]string{
				"You are evaluating two assistant answers for a context-augmented assistant benchmark.",
				"Return strict JSON only.",
				"Schema:",
				`{"relevance":{"baseline_score":1,"augmented_score":1,"winner":"baseline|augmented|tie","reason":"..."},"usefulness":{"baseline_score":1,"augmented_score":1,"winner":"baseline|augmented|tie","reason":"..."},"personalization":{"baseline_score":1,"augmented_score":1,"winner":"baseline|augmented|tie","reason":"..."},"strategy_quality":{"baseline_score":1,"augmented_score":1,"winner":"baseline|augmented|tie","reason":"..."},"overall_winner":"baseline|augmented|tie","summary":"..."}`,
				"Score from 1 to 5 where 5 is best.",
				"Personalization means using the person/topic context appropriately.",
				"Strategy quality means the answer is actionable, socially appropriate, and handles risk or scope tradeoffs well.",
				"Prefer tie when differences are marginal.",
			}, "\n"),
		}, {
			Role:    "user",
			Content: fmt.Sprintf("Case: %s\nGoal: %s\nPeople: %s\nTopics: %s\nExpected helpful cues: %s\n\nBaseline answer:\n%s\n\nMemory-augmented answer:\n%s", currentCase.Title, currentCase.Goal, strings.Join(currentCase.People, ", "), strings.Join(currentCase.Topics, ", "), strings.Join(currentCase.ExpectedAugmentedCues, ", "), baseline, augmented),
		}},
	}

	response, err := judgeClient.Generate(ctx, request)
	if err != nil {
		return nil, err
	}

	var comparison judgeComparison
	if err := json.Unmarshal([]byte(strings.TrimSpace(response.OutputText)), &comparison); err != nil {
		return nil, fmt.Errorf("parse judge response: %w", err)
	}
	return &comparison, nil
}

func summarizeReport(cases []evaluationCaseRun) evaluationSummary {
	summary := evaluationSummary{
		CaseCount:              len(cases),
		DimensionAugmentedWins: map[string]int{"relevance": 0, "usefulness": 0, "personalization": 0, "strategy_quality": 0},
		DimensionBaselineWins:  map[string]int{"relevance": 0, "usefulness": 0, "personalization": 0, "strategy_quality": 0},
		DimensionTies:          map[string]int{"relevance": 0, "usefulness": 0, "personalization": 0, "strategy_quality": 0},
		ManualFollowUps: []string{
			"Add reviewer-entered usefulness scores after reading the generated report.",
			"Add contextual appropriateness scores after checking the retrieved memories and outputs together.",
			"Review the expected memory keys before treating precision numbers as final benchmark data.",
		},
	}

	precisionSum := 0.0
	coverageSum := 0.0
	strategySum := 0.0
	strategyCount := 0
	actualSuccessSum := 0.0
	actualSuccessCount := 0
	predictionErrorSum := 0.0
	predictionErrorCount := 0
	usefulMemoriesSum := 0.0
	usefulMemoriesCount := 0

	for _, item := range cases {
		if item.Error != "" {
			summary.FailedCases++
			continue
		}
		precisionSum += item.Retrieval.PrecisionAt5
		coverageSum += item.Retrieval.Coverage
		if item.Strategy != nil && item.Strategy.LikelihoodOfSuccess > 0 {
			strategySum += item.Strategy.LikelihoodOfSuccess
			strategyCount++
		}
		if item.Outcome != nil {
			actualSuccessSum += item.Outcome.SuccessScore
			actualSuccessCount++
			usefulMemoriesSum += float64(item.Outcome.UsefulMemoriesCreated)
			usefulMemoriesCount++
			if item.Outcome.PredictionErrorAbsolute > 0 {
				predictionErrorSum += item.Outcome.PredictionErrorAbsolute
				predictionErrorCount++
			}
			if item.FollowUp != nil {
				summary.OutcomeFeedbackEvaluatedCases++
				if item.FollowUp.ChangedBehavior {
					summary.OutcomeFeedbackChangedCases++
				}
			}
		}
		if item.Judge != nil {
			updateJudgeTally(summary.DimensionAugmentedWins, summary.DimensionBaselineWins, summary.DimensionTies, "relevance", item.Judge.Relevance.Winner)
			updateJudgeTally(summary.DimensionAugmentedWins, summary.DimensionBaselineWins, summary.DimensionTies, "usefulness", item.Judge.Usefulness.Winner)
			updateJudgeTally(summary.DimensionAugmentedWins, summary.DimensionBaselineWins, summary.DimensionTies, "personalization", item.Judge.Personalization.Winner)
			updateJudgeTally(summary.DimensionAugmentedWins, summary.DimensionBaselineWins, summary.DimensionTies, "strategy_quality", item.Judge.StrategyQuality.Winner)
		}
	}

	completed := len(cases) - summary.FailedCases
	if completed > 0 {
		summary.AverageRetrievalPrecisionAt5 = precisionSum / float64(completed)
		summary.AverageRetrievalCoverage = coverageSum / float64(completed)
	}
	if strategyCount > 0 {
		summary.AverageStrategySuccessEstimate = strategySum / float64(strategyCount)
	}
	if actualSuccessCount > 0 {
		summary.AverageActualSuccessScore = actualSuccessSum / float64(actualSuccessCount)
	}
	if predictionErrorCount > 0 {
		summary.AveragePredictionErrorAbsolute = predictionErrorSum / float64(predictionErrorCount)
	}
	if usefulMemoriesCount > 0 {
		summary.AverageUsefulMemoriesPerOutcome = usefulMemoriesSum / float64(usefulMemoriesCount)
	}

	return summary
}

func updateJudgeTally(augmented, baseline, ties map[string]int, dimension, winner string) {
	switch normalizeWinner(winner) {
	case "augmented":
		augmented[dimension]++
	case "baseline":
		baseline[dimension]++
	default:
		ties[dimension]++
	}
}

func writeReportFiles(report evaluationReport, outputDir string) (string, string, error) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", "", fmt.Errorf("create output dir: %w", err)
	}
	timestamp := report.GeneratedAt.Format("20060102t150405")
	jsonBytes, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("marshal evaluation report: %w", err)
	}
	markdown := renderMarkdownReport(report)
	jsonPath := filepath.Join(outputDir, "evaluation-report-"+timestamp+".json")
	markdownPath := filepath.Join(outputDir, "evaluation-report-"+timestamp+".md")
	if err := os.WriteFile(jsonPath, jsonBytes, 0o644); err != nil {
		return "", "", fmt.Errorf("write json report: %w", err)
	}
	if err := os.WriteFile(markdownPath, []byte(markdown), 0o644); err != nil {
		return "", "", fmt.Errorf("write markdown report: %w", err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "latest.json"), jsonBytes, 0o644); err != nil {
		return "", "", fmt.Errorf("write latest json report: %w", err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "latest.md"), []byte(markdown), 0o644); err != nil {
		return "", "", fmt.Errorf("write latest markdown report: %w", err)
	}
	return jsonPath, markdownPath, nil
}

func renderMarkdownReport(report evaluationReport) string {
	var builder strings.Builder
	builder.WriteString("# Evaluation Report\n\n")
	builder.WriteString(fmt.Sprintf("Generated: %s\n\n", report.GeneratedAt.Format(time.RFC3339)))
	builder.WriteString(fmt.Sprintf("Dataset: %s\n\n", report.DatasetName))
	builder.WriteString(fmt.Sprintf("Server mode: %s\n\n", report.ServerMode))
	builder.WriteString("## Summary\n\n")
	builder.WriteString(fmt.Sprintf("- Cases: %d\n", report.Summary.CaseCount))
	builder.WriteString(fmt.Sprintf("- Failed cases: %d\n", report.Summary.FailedCases))
	builder.WriteString(fmt.Sprintf("- Average retrieval precision@5: %.2f\n", report.Summary.AverageRetrievalPrecisionAt5))
	builder.WriteString(fmt.Sprintf("- Average retrieval coverage: %.2f\n", report.Summary.AverageRetrievalCoverage))
	builder.WriteString(fmt.Sprintf("- Average strategy success estimate: %.2f\n", report.Summary.AverageStrategySuccessEstimate))
	builder.WriteString(fmt.Sprintf("- Average actual success score: %.2f\n", report.Summary.AverageActualSuccessScore))
	builder.WriteString(fmt.Sprintf("- Average prediction error absolute: %.2f\n", report.Summary.AveragePredictionErrorAbsolute))
	builder.WriteString(fmt.Sprintf("- Average useful memories created per outcome case: %.2f\n", report.Summary.AverageUsefulMemoriesPerOutcome))
	builder.WriteString(fmt.Sprintf("- Outcome feedback changed behavior: %d/%d\n", report.Summary.OutcomeFeedbackChangedCases, report.Summary.OutcomeFeedbackEvaluatedCases))
	builder.WriteString("\nJudge win counts:\n")
	for _, dimension := range []string{"relevance", "usefulness", "personalization", "strategy_quality"} {
		builder.WriteString(fmt.Sprintf("- %s: augmented=%d baseline=%d ties=%d\n", dimension, report.Summary.DimensionAugmentedWins[dimension], report.Summary.DimensionBaselineWins[dimension], report.Summary.DimensionTies[dimension]))
	}

	for _, item := range report.Cases {
		builder.WriteString("\n## ")
		builder.WriteString(item.Title)
		builder.WriteString("\n\n")
		builder.WriteString(fmt.Sprintf("- Case ID: %s\n", item.ID))
		builder.WriteString(fmt.Sprintf("- User: %s\n", item.UserExternalID))
		if item.Error != "" {
			builder.WriteString(fmt.Sprintf("- Error: %s\n", item.Error))
			continue
		}
		builder.WriteString(fmt.Sprintf("- Retrieval precision@5: %.2f\n", item.Retrieval.PrecisionAt5))
		builder.WriteString(fmt.Sprintf("- Retrieval coverage: %.2f\n", item.Retrieval.Coverage))
		builder.WriteString(fmt.Sprintf("- Baseline cue hits: %d\n", item.Baseline.CueHits))
		builder.WriteString(fmt.Sprintf("- Augmented cue hits: %d\n", item.Augmented.CueHits))
		if item.Strategy != nil {
			builder.WriteString(fmt.Sprintf("- Strategy recommendation: %s (%.2f)\n", item.Strategy.RecommendedLabel, item.Strategy.LikelihoodOfSuccess))
		}
		if item.Outcome != nil {
			builder.WriteString(fmt.Sprintf("- Actual success score: %.2f\n", item.Outcome.SuccessScore))
			builder.WriteString(fmt.Sprintf("- Useful memories created: %d\n", item.Outcome.UsefulMemoriesCreated))
		}
		if item.FollowUp != nil {
			builder.WriteString(fmt.Sprintf("- Changed behavior observed: %t\n", item.FollowUp.ChangedBehavior))
		}
		if item.Judge != nil {
			builder.WriteString(fmt.Sprintf("- Judge summary: %s\n", collapseWhitespace(item.Judge.Summary)))
		}
		builder.WriteString("\nBaseline output:\n\n")
		builder.WriteString("```")
		builder.WriteString("\n")
		builder.WriteString(item.Baseline.OutputText)
		builder.WriteString("\n```")
		builder.WriteString("\n\nAugmented output:\n\n")
		builder.WriteString("```")
		builder.WriteString("\n")
		builder.WriteString(item.Augmented.OutputText)
		builder.WriteString("\n```")
		if item.FollowUp != nil {
			builder.WriteString("\n\nFollow-up output:\n\n")
			builder.WriteString("```")
			builder.WriteString("\n")
			builder.WriteString(item.FollowUp.OutputText)
			builder.WriteString("\n```")
		}
	}

	builder.WriteString("\n## Manual Follow-Up\n\n")
	for _, line := range report.Summary.ManualFollowUps {
		builder.WriteString("- ")
		builder.WriteString(line)
		builder.WriteString("\n")
	}

	return builder.String()
}

func printSummary(report evaluationReport, jsonPath, markdownPath string) {
	fmt.Println("Evaluation report generated.")
	fmt.Printf("- Cases: %d\n", report.Summary.CaseCount)
	fmt.Printf("- Failed cases: %d\n", report.Summary.FailedCases)
	fmt.Printf("- Average retrieval precision@5: %.2f\n", report.Summary.AverageRetrievalPrecisionAt5)
	fmt.Printf("- Average actual success score: %.2f\n", report.Summary.AverageActualSuccessScore)
	fmt.Printf("- Outcome feedback changed behavior: %d/%d\n", report.Summary.OutcomeFeedbackChangedCases, report.Summary.OutcomeFeedbackEvaluatedCases)
	fmt.Printf("- JSON report: %s\n", jsonPath)
	fmt.Printf("- Markdown report: %s\n", markdownPath)
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

func recommendedStrategy(plan *scenarioPlan) scenarioStrategy {
	if plan == nil || len(plan.Strategies) == 0 {
		return scenarioStrategy{}
	}
	for _, item := range plan.Strategies {
		if item.ID == plan.RecommendedStrategyID {
			return item
		}
	}
	return plan.Strategies[0]
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

func memoryKey(memory memoryResponse) string {
	if memory.Metadata == nil {
		return ""
	}
	if value, ok := memory.Metadata["evaluation_memory_key"].(string); ok {
		return strings.TrimSpace(value)
	}
	if value, ok := memory.Metadata["demo_memory_key"].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func cueHits(output string, cues []string) (int, []string) {
	hits := 0
	misses := make([]string, 0)
	for _, cue := range cues {
		if cuePresent(output, cue) {
			hits++
			continue
		}
		misses = append(misses, cue)
	}
	return hits, misses
}

func cuePresent(output, cue string) bool {
	needle := strings.ToLower(strings.TrimSpace(cue))
	if needle == "" {
		return false
	}
	haystack := strings.ToLower(output)
	if strings.Contains(haystack, needle) {
		return true
	}
	needleTerms := strings.Fields(needle)
	if len(needleTerms) == 0 {
		return false
	}
	for _, term := range needleTerms {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}

func mentionsInText(output string, items []string) []string {
	mentions := make([]string, 0)
	for _, item := range items {
		if cuePresent(output, item) {
			mentions = append(mentions, item)
		}
	}
	return mentions
}

func followUpHasOutcomeMemory(response debugContextResponse) bool {
	if response.CurrentContextPacket == nil {
		return false
	}
	for _, memory := range response.CurrentContextPacket.MemoryContext {
		if strings.EqualFold(strings.TrimSpace(memory.Type), "outcome") {
			return true
		}
		if cuePresent(memory.Summary, "asked me to narrow the request") || cuePresent(memory.Summary, "API section") {
			return true
		}
	}
	return false
}

func normalizeWinner(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "augmented", "memory", "memory_augmented":
		return "augmented"
	case "baseline", "stateless":
		return "baseline"
	default:
		return "tie"
	}
}

func normalizeID(value string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	trimmed = strings.ReplaceAll(trimmed, " ", "-")
	trimmed = strings.ReplaceAll(trimmed, "_", "-")
	return trimmed
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

func collapseWhitespace(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
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
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/debug/context?session_id="+url.QueryEscape(sessionID), nil)
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
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(payload))
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
