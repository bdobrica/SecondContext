package beliefs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bdobrica/SecondContext/internal/config"
	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/llm"
	"github.com/bdobrica/SecondContext/internal/models"
	"github.com/bdobrica/SecondContext/internal/prompts"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	cfg  config.Config
	pool *pgxpool.Pool
	llm  llm.Client
}

type Error struct {
	StatusCode int
	Message    string
	Type       string
	Code       string
	Param      string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}

	return e.Message
}

type beliefObservationEnvelope struct {
	Beliefs []beliefObservation `json:"beliefs"`
}

type beliefObservation struct {
	Claim           string   `json:"claim"`
	TopicName       string   `json:"topic_name"`
	TopicAliases    []string `json:"topic_aliases"`
	Stance          string   `json:"stance"`
	Confidence      float64  `json:"confidence"`
	EvidenceSummary string   `json:"evidence_summary"`
}

type BeliefView struct {
	Belief           models.Belief
	Topic            models.Topic
	HasContradiction bool
	Summary          string
}

type ListBeliefsParams struct {
	UserExternalID string
	TopicID        string
	TopicName      string
	Limit          int
}

const MaxListResults = 100

func NewService(cfg config.Config, pool *pgxpool.Pool, client llm.Client) *Service {
	return &Service{cfg: cfg, pool: pool, llm: client}
}

func (s *Service) ObserveMemory(ctx context.Context, memory models.MemoryItem) error {
	if s.pool == nil {
		return nil
	}
	if memory.BeliefImpact <= 0 && !isBeliefBearingMemoryType(memory.MemoryType) {
		return nil
	}

	observation, err := s.extractObservation(ctx, memory)
	if err != nil {
		return err
	}
	if len(observation.Beliefs) == 0 {
		return nil
	}

	beliefsRepo := db.NewBeliefRepository(s.pool)
	topicRepo := db.NewTopicRepository(s.pool)
	now := time.Now().UTC()

	for _, item := range observation.Beliefs {
		topic, err := resolveTopic(ctx, topicRepo, memory.UserID, item.TopicName, item.TopicAliases, memory.Topics)
		if err != nil {
			return err
		}

		topicID := ""
		if strings.TrimSpace(topic.ID) != "" {
			topicID = topic.ID
		}

		existing, err := beliefsRepo.GetByClaimAndTopic(ctx, memory.UserID, item.Claim, topicID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		merged := mergeBeliefObservation(existing, item, memory.ID, now)
		if _, err := beliefsRepo.Save(ctx, db.SaveBeliefParams{
			ID:                merged.ID,
			UserID:            memory.UserID,
			TopicID:           topicID,
			Claim:             merged.Claim,
			Stance:            merged.Stance,
			Confidence:        merged.Confidence,
			EvidenceMemoryIDs: merged.EvidenceMemoryIDs,
			LastUpdatedAt:     merged.LastUpdatedAt,
			Metadata:          merged.Metadata,
		}); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) ListBeliefs(ctx context.Context, params ListBeliefsParams) ([]BeliefView, error) {
	if s.pool == nil {
		return nil, &Error{StatusCode: http.StatusInternalServerError, Message: "postgres is not configured", Type: "server_error", Code: "postgres_disabled"}
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > MaxListResults {
		return nil, &Error{StatusCode: http.StatusBadRequest, Message: "limit must not exceed 100", Type: "invalid_request_error", Code: "invalid_limit", Param: "limit"}
	}

	user, err := s.resolveUser(ctx, params.UserExternalID)
	if err != nil {
		return nil, err
	}

	beliefsRepo := db.NewBeliefRepository(s.pool)
	topicRepo := db.NewTopicRepository(s.pool)

	var beliefsList []models.Belief
	if strings.TrimSpace(params.TopicID) != "" {
		beliefsList, err = beliefsRepo.ListByTopic(ctx, user.ID, strings.TrimSpace(params.TopicID), int32(limit))
		if err != nil {
			return nil, err
		}
	} else if strings.TrimSpace(params.TopicName) != "" {
		topic, err := topicRepo.GetByName(ctx, user.ID, params.TopicName)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil
			}
			return nil, err
		}
		beliefsList, err = beliefsRepo.ListByTopic(ctx, user.ID, topic.ID, int32(limit))
		if err != nil {
			return nil, err
		}
	} else {
		beliefsList, err = beliefsRepo.ListByUser(ctx, user.ID, int32(limit))
		if err != nil {
			return nil, err
		}
	}

	return s.toViews(ctx, beliefsList), nil
}

func (s *Service) BuildPromptContext(ctx context.Context, userExternalID string, topics []string, limit int) ([]string, error) {
	if s.pool == nil || limit <= 0 {
		return nil, nil
	}

	user, err := s.resolveUser(ctx, userExternalID)
	if err != nil {
		return nil, err
	}

	beliefsRepo := db.NewBeliefRepository(s.pool)
	topicRepo := db.NewTopicRepository(s.pool)
	lines := make([]string, 0, limit)
	seen := make(map[string]struct{})

	for _, topicName := range uniqueStrings(topics) {
		topic, err := topicRepo.GetByName(ctx, user.ID, topicName)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return nil, err
		}

		beliefsList, err := beliefsRepo.ListByTopic(ctx, user.ID, topic.ID, int32(limit))
		if err != nil {
			return nil, err
		}
		for _, view := range s.toViews(ctx, beliefsList) {
			key := strings.ToLower(strings.TrimSpace(view.Summary))
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			lines = append(lines, view.Summary)
			if len(lines) >= limit {
				return lines, nil
			}
		}
	}

	if len(lines) > 0 {
		return lines, nil
	}

	beliefsList, err := beliefsRepo.ListByUser(ctx, user.ID, int32(limit))
	if err != nil {
		return nil, err
	}
	for _, view := range s.toViews(ctx, beliefsList) {
		key := strings.ToLower(strings.TrimSpace(view.Summary))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		lines = append(lines, view.Summary)
		if len(lines) >= limit {
			break
		}
	}

	return lines, nil
}

func (s *Service) extractObservation(ctx context.Context, memory models.MemoryItem) (beliefObservationEnvelope, error) {
	response, err := s.llm.Generate(ctx, llm.GenerateRequest{
		Model: s.cfg.OpenAI.ChatModel,
		Messages: []llm.Message{{
			Role:    "system",
			Content: prompts.BeliefExtractionSystemPrompt(),
		}, {
			Role:    "user",
			Content: prompts.BuildBeliefExtractionUserPrompt(memory.RawText, memory.Summary, memory.Topics),
		}},
	})
	if err != nil {
		return beliefObservationEnvelope{}, err
	}

	observation, parseErr := parseObservation(response.OutputText)
	if parseErr == nil {
		return observation, nil
	}

	repairResponse, err := s.llm.Generate(ctx, llm.GenerateRequest{
		Model: s.cfg.OpenAI.ChatModel,
		Messages: []llm.Message{{
			Role:    "system",
			Content: prompts.BeliefExtractionSystemPrompt(),
		}, {
			Role:    "user",
			Content: prompts.BuildBeliefExtractionRepairPrompt(response.OutputText),
		}},
	})
	if err != nil {
		return beliefObservationEnvelope{}, parseErr
	}

	return parseObservation(repairResponse.OutputText)
}

func parseObservation(raw string) (beliefObservationEnvelope, error) {
	var observation beliefObservationEnvelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &observation); err != nil {
		return beliefObservationEnvelope{}, err
	}

	filtered := make([]beliefObservation, 0, len(observation.Beliefs))
	for _, item := range observation.Beliefs {
		if strings.TrimSpace(item.Claim) == "" {
			continue
		}
		item.TopicAliases = uniqueStrings(item.TopicAliases)
		item.Stance = normalizeStance(item.Stance)
		item.Confidence = clampScore(item.Confidence)
		filtered = append(filtered, item)
	}
	observation.Beliefs = filtered

	return observation, nil
}

func resolveTopic(ctx context.Context, repo *db.TopicRepository, userID, observed string, aliases, candidates []string) (models.Topic, error) {
	name := strings.TrimSpace(observed)
	if name == "" && len(uniqueStrings(candidates)) == 1 {
		name = strings.TrimSpace(candidates[0])
	}
	if name == "" {
		return models.Topic{}, nil
	}

	if candidate := matchCandidate(name, candidates); candidate != "" {
		name = candidate
	} else {
		for _, alias := range aliases {
			if candidate := matchCandidate(alias, candidates); candidate != "" {
				name = candidate
				break
			}
		}
	}

	existing, err := repo.GetByName(ctx, userID, name)
	if err == nil {
		return repo.Upsert(ctx, db.UpsertTopicParams{UserID: userID, Name: existing.Name, Aliases: mergeStrings(existing.Aliases, aliases), Metadata: existing.Metadata})
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return models.Topic{}, err
	}

	return repo.Upsert(ctx, db.UpsertTopicParams{UserID: userID, Name: name, Aliases: aliases})
}

func mergeBeliefObservation(existing models.Belief, observation beliefObservation, memoryID string, now time.Time) models.Belief {
	for _, evidenceID := range existing.EvidenceMemoryIDs {
		if evidenceID == memoryID {
			return existing
		}
	}
	if strings.TrimSpace(existing.UserID) == "" {
		return models.Belief{
			ID:                existing.ID,
			UserID:            existing.UserID,
			TopicID:           existing.TopicID,
			Claim:             strings.TrimSpace(observation.Claim),
			Stance:            normalizeStance(observation.Stance),
			Confidence:        clampScore(observation.Confidence),
			EvidenceMemoryIDs: uniqueStrings([]string{memoryID}),
			LastUpdatedAt:     now,
			Metadata:          mergeBeliefMetadata(existing.Metadata, observation, false),
		}
	}

	mergedEvidence := mergeStrings(existing.EvidenceMemoryIDs, []string{memoryID})
	conflict := stancesConflict(existing.Stance, observation.Stance)
	mergedStance := mergeStance(existing.Stance, observation.Stance)
	mergedConfidence := averageConfidence(existing.Confidence, len(existing.EvidenceMemoryIDs), observation.Confidence)
	if conflict && mergedConfidence > 0.6 {
		mergedConfidence = 0.6
	}

	return models.Belief{
		ID:                existing.ID,
		UserID:            existing.UserID,
		TopicID:           existing.TopicID,
		Claim:             firstNonEmpty(strings.TrimSpace(existing.Claim), strings.TrimSpace(observation.Claim)),
		Stance:            mergedStance,
		Confidence:        clampScore(mergedConfidence),
		EvidenceMemoryIDs: mergedEvidence,
		LastUpdatedAt:     now,
		Metadata:          mergeBeliefMetadata(existing.Metadata, observation, conflict),
	}
}

func mergeBeliefMetadata(raw json.RawMessage, observation beliefObservation, conflict bool) json.RawMessage {
	payload := make(map[string]any)
	if len(strings.TrimSpace(string(raw))) > 0 {
		_ = json.Unmarshal(raw, &payload)
	}

	stanceHistory := metadataStringSlice(payload, "stance_history")
	stanceHistory = mergeStrings(stanceHistory, []string{normalizeStance(observation.Stance)})
	payload["stance_history"] = stanceHistory
	payload["contradiction_detected"] = metadataBool(payload, "contradiction_detected") || conflict
	if summary := strings.TrimSpace(observation.EvidenceSummary); summary != "" {
		summaries := metadataStringSlice(payload, "evidence_summaries")
		payload["evidence_summaries"] = mergeStrings(summaries, []string{summary})
		payload["last_evidence_summary"] = summary
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{}`)
	}

	return encoded
}

func (s *Service) toViews(ctx context.Context, beliefsList []models.Belief) []BeliefView {
	topicRepo := db.NewTopicRepository(s.pool)
	views := make([]BeliefView, 0, len(beliefsList))
	for _, belief := range beliefsList {
		var topic models.Topic
		if strings.TrimSpace(belief.TopicID) != "" {
			loadedTopic, err := topicRepo.GetByID(ctx, belief.TopicID)
			if err == nil {
				topic = loadedTopic
			}
		}

		view := BeliefView{
			Belief:           belief,
			Topic:            topic,
			HasContradiction: hasContradiction(belief.Metadata),
		}
		view.Summary = summarizeBelief(view)
		views = append(views, view)
	}

	return views
}

func summarizeBelief(view BeliefView) string {
	topicName := strings.TrimSpace(view.Topic.Name)
	if topicName == "" {
		topicName = "general"
	}
	count := len(view.Belief.EvidenceMemoryIDs)
	stanceText := stanceSummary(view.Belief.Stance)
	summary := fmt.Sprintf("%s: claim %q is %s (confidence=%s, evidence_count=%d).", topicName, view.Belief.Claim, stanceText, formatScore(view.Belief.Confidence), count)
	if view.HasContradiction {
		summary += " Conflicting evidence exists, so treat this as uncertain."
	}

	return summary
}

func stanceSummary(stance string) string {
	switch normalizeStance(stance) {
	case "supports":
		return "currently supported"
	case "weakens":
		return "currently weakened"
	case "contradicts":
		return "currently contradicted"
	default:
		return "currently uncertain"
	}
}

func (s *Service) resolveUser(ctx context.Context, externalID string) (models.User, error) {
	resolvedExternalID := strings.TrimSpace(externalID)
	if resolvedExternalID == "" {
		resolvedExternalID = s.cfg.Dev.UserExternalID
	}
	resolvedName := s.cfg.Dev.UserName
	resolvedEmail := s.cfg.Dev.UserEmail
	if resolvedExternalID != s.cfg.Dev.UserExternalID {
		resolvedName = resolvedExternalID
		resolvedEmail = ""
	}

	return db.NewUserRepository(s.pool).Ensure(ctx, db.EnsureUserParams{
		ExternalID:  resolvedExternalID,
		Email:       resolvedEmail,
		DisplayName: resolvedName,
	})
}

func isBeliefBearingMemoryType(memoryType string) bool {
	switch strings.ToLower(strings.TrimSpace(memoryType)) {
	case "belief_update", "outcome":
		return true
	default:
		return false
	}
}

func normalizeStance(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "supports", "support":
		return "supports"
	case "weakens", "weaken", "questions":
		return "weakens"
	case "contradicts", "contradict", "refutes":
		return "contradicts"
	default:
		return "unknown"
	}
}

func stancesConflict(left, right string) bool {
	left = normalizeStance(left)
	right = normalizeStance(right)
	if left == "" || right == "" || left == right {
		return false
	}
	if left == "unknown" || right == "unknown" {
		return false
	}

	return true
}

func mergeStance(existing, observed string) string {
	existing = normalizeStance(existing)
	observed = normalizeStance(observed)
	if existing == "" || existing == "unknown" {
		return observed
	}
	if observed == "" || observed == "unknown" {
		return existing
	}
	if existing == observed {
		return existing
	}

	return "unknown"
}

func averageConfidence(existing float64, count int, observed float64) float64 {
	if count <= 0 {
		return clampScore(observed)
	}

	return clampScore(((clampScore(existing) * float64(count)) + clampScore(observed)) / float64(count+1))
}

func hasContradiction(raw json.RawMessage) bool {
	var payload map[string]any
	if len(strings.TrimSpace(string(raw))) == 0 {
		return false
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return false
	}
	return metadataBool(payload, "contradiction_detected")
}

func metadataStringSlice(payload map[string]any, key string) []string {
	raw, ok := payload[key]
	if !ok {
		return nil
	}
	values, ok := raw.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			continue
		}
		result = append(result, text)
	}
	return uniqueStrings(result)
}

func metadataBool(payload map[string]any, key string) bool {
	value, ok := payload[key]
	if !ok {
		return false
	}
	parsed, ok := value.(bool)
	if !ok {
		return false
	}
	return parsed
}

func matchCandidate(value string, candidates []string) string {
	normalizedValue := normalizeKey(value)
	if normalizedValue == "" {
		return ""
	}
	for _, candidate := range candidates {
		if normalizeKey(candidate) == normalizedValue {
			return strings.TrimSpace(candidate)
		}
	}
	return ""
}

func normalizeKey(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := normalizeKey(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func mergeStrings(left, right []string) []string {
	merged := append(append([]string{}, left...), right...)
	return uniqueStrings(merged)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func clampScore(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func formatScore(value float64) string {
	formatted := fmt.Sprintf("%.2f", clampScore(value))
	formatted = strings.TrimRight(formatted, "0")
	formatted = strings.TrimRight(formatted, ".")
	if formatted == "" {
		return "0"
	}
	return formatted
}
