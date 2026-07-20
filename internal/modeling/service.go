package modeling

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
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

type updateObservation struct {
	Pairs []observationPair `json:"pairs"`
}

type observationPair struct {
	PersonName      string   `json:"person_name"`
	PersonAliases   []string `json:"person_aliases"`
	TopicName       string   `json:"topic_name"`
	TopicAliases    []string `json:"topic_aliases"`
	Niceness        float64  `json:"niceness"`
	Readiness       float64  `json:"readiness"`
	Competence      float64  `json:"competence"`
	Capacity        float64  `json:"capacity"`
	Confidence      float64  `json:"confidence"`
	EvidenceSummary string   `json:"evidence_summary"`
	LastObservedAt  string   `json:"last_observed_at"`
}

type PersonModelView struct {
	Model             models.PersonTopicModel
	Topic             models.Topic
	EvidenceMemoryIDs []string
	Summary           string
}

type PersonProfile struct {
	Person models.Person
	Models []PersonModelView
}

type UpdatePersonModelParams struct {
	TopicID           string
	TopicName         string
	TopicAliases      []string
	PersonAliases     []string
	Niceness          *float64
	Readiness         *float64
	Competence        *float64
	Capacity          *float64
	Confidence        *float64
	EvidenceCount     *int
	EvidenceMemoryIDs []string
	LastObservedAt    *time.Time
}

func NewService(cfg config.Config, pool *pgxpool.Pool, client llm.Client) *Service {
	return &Service{cfg: cfg, pool: pool, llm: client}
}

func (s *Service) ObserveMemory(ctx context.Context, memory models.MemoryItem) error {
	if s.pool == nil || len(memory.People) == 0 || len(memory.Topics) == 0 {
		return nil
	}

	observation, err := s.extractObservation(ctx, memory)
	if err != nil {
		return err
	}

	peopleRepo := db.NewPersonRepository(s.pool)
	topicRepo := db.NewTopicRepository(s.pool)
	modelsRepo := db.NewPersonTopicModelRepository(s.pool)

	for _, pair := range observation.Pairs {
		personName, personAliases, ok := resolveCandidate(pair.PersonName, pair.PersonAliases, memory.People)
		if !ok {
			continue
		}
		topicName, topicAliases, ok := resolveCandidate(pair.TopicName, pair.TopicAliases, memory.Topics)
		if !ok {
			continue
		}

		person, err := upsertObservedPerson(ctx, peopleRepo, memory.UserID, personName, personAliases)
		if err != nil {
			return err
		}
		topic, err := upsertObservedTopic(ctx, topicRepo, memory.UserID, topicName, topicAliases)
		if err != nil {
			return err
		}

		existing, err := modelsRepo.GetByPersonAndTopic(ctx, memory.UserID, person.ID, topic.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		merged := mergeObservation(existing, pair, memory.ID)
		if _, err := modelsRepo.Save(ctx, db.SavePersonTopicModelParams{
			UserID:         memory.UserID,
			PersonID:       person.ID,
			TopicID:        topic.ID,
			Niceness:       merged.Niceness,
			Readiness:      merged.Readiness,
			Competence:     merged.Competence,
			Capacity:       merged.Capacity,
			Confidence:     merged.Confidence,
			EvidenceCount:  merged.EvidenceCount,
			LastObservedAt: merged.LastObservedAt,
			Metadata:       merged.Metadata,
		}); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) BuildPromptContext(ctx context.Context, userExternalID string, people, topics []string, limit int) ([]string, error) {
	if s.pool == nil || limit <= 0 {
		return nil, nil
	}

	user, err := s.resolveUser(ctx, userExternalID)
	if err != nil {
		return nil, err
	}

	peopleRepo := db.NewPersonRepository(s.pool)
	modelsRepo := db.NewPersonTopicModelRepository(s.pool)
	topicRepo := db.NewTopicRepository(s.pool)
	topicFilter := make(map[string]struct{}, len(topics))
	for _, topic := range uniqueStrings(topics) {
		topicFilter[normalizeKey(topic)] = struct{}{}
	}

	lines := make([]string, 0, limit)
	seen := make(map[string]struct{})
	for _, personName := range uniqueStrings(people) {
		person, err := peopleRepo.GetByName(ctx, user.ID, personName)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return nil, err
		}

		personModels, err := modelsRepo.ListByPerson(ctx, user.ID, person.ID)
		if err != nil {
			return nil, err
		}

		for _, modelRecord := range personModels {
			topic, err := topicRepo.GetByID(ctx, modelRecord.TopicID)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					continue
				}
				return nil, err
			}
			if len(topicFilter) > 0 {
				if _, ok := topicFilter[normalizeKey(topic.Name)]; !ok {
					continue
				}
			}

			line := safeSummary(person.Name, topic.Name, modelRecord)
			key := strings.ToLower(line)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			lines = append(lines, line)
			if len(lines) >= limit {
				return lines, nil
			}
		}
	}

	return lines, nil
}

func (s *Service) GetPersonProfile(ctx context.Context, personID, topicID, topicName string) (PersonProfile, error) {
	if s.pool == nil {
		return PersonProfile{}, &Error{StatusCode: http.StatusInternalServerError, Message: "postgres is not configured", Type: "server_error", Code: "postgres_disabled"}
	}

	person, err := db.NewPersonRepository(s.pool).GetByID(ctx, personID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PersonProfile{}, &Error{StatusCode: http.StatusNotFound, Message: "person not found", Type: "invalid_request_error", Code: "person_not_found", Param: "personID"}
		}
		return PersonProfile{}, err
	}

	profile, err := s.loadProfile(ctx, person, topicID, topicName)
	if err != nil {
		return PersonProfile{}, err
	}

	return profile, nil
}

func (s *Service) UpdatePersonModel(ctx context.Context, personID string, params UpdatePersonModelParams) (PersonProfile, error) {
	if s.pool == nil {
		return PersonProfile{}, &Error{StatusCode: http.StatusInternalServerError, Message: "postgres is not configured", Type: "server_error", Code: "postgres_disabled"}
	}

	peopleRepo := db.NewPersonRepository(s.pool)
	topicRepo := db.NewTopicRepository(s.pool)
	modelsRepo := db.NewPersonTopicModelRepository(s.pool)

	person, err := peopleRepo.GetByID(ctx, personID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PersonProfile{}, &Error{StatusCode: http.StatusNotFound, Message: "person not found", Type: "invalid_request_error", Code: "person_not_found", Param: "personID"}
		}
		return PersonProfile{}, err
	}

	if len(params.PersonAliases) > 0 {
		person, err = peopleRepo.Upsert(ctx, db.UpsertPersonParams{
			UserID:   person.UserID,
			Name:     person.Name,
			Aliases:  mergeAliases(person.Aliases, params.PersonAliases),
			Metadata: person.Metadata,
		})
		if err != nil {
			return PersonProfile{}, err
		}
	}

	topic, err := resolveTopicForUpdate(ctx, topicRepo, person.UserID, params.TopicID, params.TopicName)
	if err != nil {
		return PersonProfile{}, err
	}

	if len(params.TopicAliases) > 0 {
		topic, err = topicRepo.Upsert(ctx, db.UpsertTopicParams{
			UserID:   topic.UserID,
			Name:     topic.Name,
			Aliases:  mergeAliases(topic.Aliases, params.TopicAliases),
			Metadata: topic.Metadata,
		})
		if err != nil {
			return PersonProfile{}, err
		}
	}

	existing, err := modelsRepo.GetByPersonAndTopic(ctx, person.UserID, person.ID, topic.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return PersonProfile{}, err
	}

	merged := existing
	if merged.UserID == "" {
		merged.UserID = person.UserID
		merged.PersonID = person.ID
		merged.TopicID = topic.ID
		merged.Metadata = json.RawMessage(`{}`)
	}
	merged.Niceness = valueOrDefault(params.Niceness, merged.Niceness)
	merged.Readiness = valueOrDefault(params.Readiness, merged.Readiness)
	merged.Competence = valueOrDefault(params.Competence, merged.Competence)
	merged.Capacity = valueOrDefault(params.Capacity, merged.Capacity)
	merged.Confidence = valueOrDefault(params.Confidence, merged.Confidence)
	if params.EvidenceCount != nil {
		merged.EvidenceCount = maxInt(*params.EvidenceCount, 0)
	}
	merged.LastObservedAt = latestTime(merged.LastObservedAt, params.LastObservedAt)
	merged.Metadata = mergeEvidenceMetadata(merged.Metadata, params.EvidenceMemoryIDs, "")
	if params.EvidenceCount == nil && len(params.EvidenceMemoryIDs) > 0 && merged.EvidenceCount < len(params.EvidenceMemoryIDs) {
		merged.EvidenceCount = len(params.EvidenceMemoryIDs)
	}

	if _, err := modelsRepo.Save(ctx, db.SavePersonTopicModelParams{
		UserID:         person.UserID,
		PersonID:       person.ID,
		TopicID:        topic.ID,
		Niceness:       clampScore(merged.Niceness),
		Readiness:      clampScore(merged.Readiness),
		Competence:     clampScore(merged.Competence),
		Capacity:       clampScore(merged.Capacity),
		Confidence:     clampScore(merged.Confidence),
		EvidenceCount:  maxInt(merged.EvidenceCount, 0),
		LastObservedAt: merged.LastObservedAt,
		Metadata:       merged.Metadata,
	}); err != nil {
		return PersonProfile{}, err
	}

	return s.loadProfile(ctx, person, topic.ID, "")
}

func (s *Service) loadProfile(ctx context.Context, person models.Person, topicID, topicName string) (PersonProfile, error) {
	modelsRepo := db.NewPersonTopicModelRepository(s.pool)
	topicRepo := db.NewTopicRepository(s.pool)

	filterTopicID := strings.TrimSpace(topicID)
	if filterTopicID == "" && strings.TrimSpace(topicName) != "" {
		topic, err := topicRepo.GetByName(ctx, person.UserID, topicName)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return PersonProfile{Person: person}, nil
			}
			return PersonProfile{}, err
		}
		filterTopicID = topic.ID
	}

	modelRecords, err := modelsRepo.ListByPerson(ctx, person.UserID, person.ID)
	if err != nil {
		return PersonProfile{}, err
	}

	views := make([]PersonModelView, 0, len(modelRecords))
	for _, modelRecord := range modelRecords {
		if filterTopicID != "" && modelRecord.TopicID != filterTopicID {
			continue
		}
		topic, err := topicRepo.GetByID(ctx, modelRecord.TopicID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return PersonProfile{}, err
		}
		views = append(views, PersonModelView{
			Model:             modelRecord,
			Topic:             topic,
			EvidenceMemoryIDs: extractEvidenceMemoryIDs(modelRecord.Metadata),
			Summary:           safeSummary(person.Name, topic.Name, modelRecord),
		})
	}

	return PersonProfile{Person: person, Models: views}, nil
}

func (s *Service) extractObservation(ctx context.Context, memory models.MemoryItem) (updateObservation, error) {
	response, err := s.llm.Generate(ctx, llm.GenerateRequest{
		Model: s.cfg.OpenAI.ChatModel,
		Messages: []llm.Message{{
			Role:    "system",
			Content: prompts.PersonTopicModelSystemPrompt(),
		}, {
			Role:    "user",
			Content: prompts.BuildPersonTopicModelUserPrompt(memory.RawText, memory.Summary, memory.People, memory.Topics),
		}},
	})
	if err != nil {
		return updateObservation{}, err
	}

	observation, parseErr := parseObservation(response.OutputText)
	if parseErr == nil {
		return observation, nil
	}

	repairResponse, err := s.llm.Generate(ctx, llm.GenerateRequest{
		Model: s.cfg.OpenAI.ChatModel,
		Messages: []llm.Message{{
			Role:    "system",
			Content: prompts.PersonTopicModelSystemPrompt(),
		}, {
			Role:    "user",
			Content: prompts.BuildPersonTopicModelRepairPrompt(response.OutputText),
		}},
	})
	if err != nil {
		return updateObservation{}, parseErr
	}

	return parseObservation(repairResponse.OutputText)
}

func parseObservation(raw string) (updateObservation, error) {
	var observation updateObservation
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &observation); err != nil {
		return updateObservation{}, err
	}

	filtered := make([]observationPair, 0, len(observation.Pairs))
	for _, pair := range observation.Pairs {
		if strings.TrimSpace(pair.PersonName) == "" || strings.TrimSpace(pair.TopicName) == "" {
			continue
		}
		pair.PersonAliases = uniqueStrings(pair.PersonAliases)
		pair.TopicAliases = uniqueStrings(pair.TopicAliases)
		pair.Niceness = clampScore(pair.Niceness)
		pair.Readiness = clampScore(pair.Readiness)
		pair.Competence = clampScore(pair.Competence)
		pair.Capacity = clampScore(pair.Capacity)
		pair.Confidence = clampScore(pair.Confidence)
		filtered = append(filtered, pair)
	}
	observation.Pairs = filtered

	return observation, nil
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

func resolveCandidate(observed string, aliases, candidates []string) (string, []string, bool) {
	match := matchCandidate(observed, candidates)
	if match == "" {
		for _, alias := range aliases {
			match = matchCandidate(alias, candidates)
			if match != "" {
				break
			}
		}
	}
	if match == "" {
		return "", nil, false
	}

	return match, mergeAliases(nil, aliases), true
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

func upsertObservedPerson(ctx context.Context, repo *db.PersonRepository, userID, name string, aliases []string) (models.Person, error) {
	existing, err := repo.GetByName(ctx, userID, name)
	if err == nil {
		return repo.Upsert(ctx, db.UpsertPersonParams{UserID: userID, Name: existing.Name, Aliases: mergeAliases(existing.Aliases, aliases), Metadata: existing.Metadata})
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return models.Person{}, err
	}

	return repo.Upsert(ctx, db.UpsertPersonParams{UserID: userID, Name: name, Aliases: mergeAliases(nil, aliases)})
}

func upsertObservedTopic(ctx context.Context, repo *db.TopicRepository, userID, name string, aliases []string) (models.Topic, error) {
	existing, err := repo.GetByName(ctx, userID, name)
	if err == nil {
		return repo.Upsert(ctx, db.UpsertTopicParams{UserID: userID, Name: existing.Name, Aliases: mergeAliases(existing.Aliases, aliases), Metadata: existing.Metadata})
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return models.Topic{}, err
	}

	return repo.Upsert(ctx, db.UpsertTopicParams{UserID: userID, Name: name, Aliases: mergeAliases(nil, aliases)})
}

func resolveTopicForUpdate(ctx context.Context, repo *db.TopicRepository, userID, topicID, topicName string) (models.Topic, error) {
	if strings.TrimSpace(topicID) != "" {
		topic, err := repo.GetByID(ctx, topicID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return models.Topic{}, &Error{StatusCode: http.StatusNotFound, Message: "topic not found", Type: "invalid_request_error", Code: "topic_not_found", Param: "topic_id"}
			}
			return models.Topic{}, err
		}
		if topic.UserID != userID {
			return models.Topic{}, &Error{StatusCode: http.StatusNotFound, Message: "topic not found", Type: "invalid_request_error", Code: "topic_not_found", Param: "topic_id"}
		}
		return topic, nil
	}
	if strings.TrimSpace(topicName) == "" {
		return models.Topic{}, &Error{StatusCode: http.StatusBadRequest, Message: "topic_id or topic_name is required", Type: "invalid_request_error", Code: "missing_topic", Param: "topic_name"}
	}

	topic, err := repo.GetByName(ctx, userID, topicName)
	if err == nil {
		return topic, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return models.Topic{}, err
	}

	return repo.Upsert(ctx, db.UpsertTopicParams{UserID: userID, Name: strings.TrimSpace(topicName)})
}

func mergeObservation(existing models.PersonTopicModel, pair observationPair, memoryID string) models.PersonTopicModel {
	for _, evidenceID := range extractEvidenceMemoryIDs(existing.Metadata) {
		if evidenceID == memoryID {
			return existing
		}
	}
	currentCount := maxInt(existing.EvidenceCount, 0)
	newCount := currentCount + 1
	lastObservedAt := parseObservedAt(pair.LastObservedAt)
	if lastObservedAt == nil {
		now := time.Now().UTC()
		lastObservedAt = &now
	}

	metadata := mergeEvidenceMetadata(existing.Metadata, []string{memoryID}, pair.EvidenceSummary)
	if currentCount == 0 {
		return models.PersonTopicModel{
			UserID:         existing.UserID,
			PersonID:       existing.PersonID,
			TopicID:        existing.TopicID,
			Niceness:       clampScore(pair.Niceness),
			Readiness:      clampScore(pair.Readiness),
			Competence:     clampScore(pair.Competence),
			Capacity:       clampScore(pair.Capacity),
			Confidence:     clampScore(pair.Confidence),
			EvidenceCount:  1,
			LastObservedAt: lastObservedAt,
			Metadata:       metadata,
		}
	}

	return models.PersonTopicModel{
		ID:             existing.ID,
		UserID:         existing.UserID,
		PersonID:       existing.PersonID,
		TopicID:        existing.TopicID,
		Niceness:       averageScore(existing.Niceness, currentCount, pair.Niceness),
		Readiness:      averageScore(existing.Readiness, currentCount, pair.Readiness),
		Competence:     averageScore(existing.Competence, currentCount, pair.Competence),
		Capacity:       averageScore(existing.Capacity, currentCount, pair.Capacity),
		Confidence:     averageScore(existing.Confidence, currentCount, pair.Confidence),
		EvidenceCount:  newCount,
		LastObservedAt: latestTime(existing.LastObservedAt, lastObservedAt),
		Metadata:       metadata,
	}
}

func averageScore(existing float64, count int, observed float64) float64 {
	if count <= 0 {
		return clampScore(observed)
	}

	return clampScore(((existing * float64(count)) + observed) / float64(count+1))
}

func parseObservedAt(raw string) *time.Time {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	timestamp, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return nil
	}
	parsed := timestamp.UTC()
	return &parsed
}

func mergeEvidenceMetadata(raw json.RawMessage, memoryIDs []string, summary string) json.RawMessage {
	payload := make(map[string]any)
	if len(strings.TrimSpace(string(raw))) > 0 {
		_ = json.Unmarshal(raw, &payload)
	}

	existingIDs := extractEvidenceMemoryIDs(raw)
	payload["evidence_memory_ids"] = mergeAliases(existingIDs, memoryIDs)
	if trimmed := strings.TrimSpace(summary); trimmed != "" {
		payload["last_evidence_summary"] = trimmed
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{}`)
	}

	return encoded
}

func extractEvidenceMemoryIDs(raw json.RawMessage) []string {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	values, ok := payload["evidence_memory_ids"].([]any)
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

func safeSummary(personName, topicName string, model models.PersonTopicModel) string {
	traits := []string{
		"niceness " + bandLabel(model.Niceness, "low", "mixed", "high"),
		"readiness " + bandLabel(model.Readiness, "low", "mixed", "high"),
		"competence " + bandLabel(model.Competence, "low", "mixed", "high"),
		"capacity " + bandLabel(model.Capacity, "low", "mixed", "high"),
	}

	return strings.TrimSpace(personName + " on " + topicName + ": working estimate only; " + strings.Join(traits, ", ") + "; confidence=" + formatScore(model.Confidence) + "; evidence_count=" + itoa(model.EvidenceCount) + ". Treat as topic-specific and uncertain.")
}

func bandLabel(score float64, low, mid, high string) string {
	switch {
	case score >= 0.7:
		return high
	case score <= 0.35:
		return low
	default:
		return mid
	}
}

func formatScore(value float64) string {
	return strings.TrimRight(strings.TrimRight(strconvFormatFloat(clampScore(value)), "0"), ".")
}

func valueOrDefault(value *float64, fallback float64) float64 {
	if value == nil {
		return fallback
	}

	return clampScore(*value)
}

func latestTime(left, right *time.Time) *time.Time {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	if right.After(*left) {
		return right
	}

	return left
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

func normalizeKey(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func mergeAliases(existing, updates []string) []string {
	merged := append(append([]string{}, existing...), updates...)
	return uniqueStrings(merged)
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

func maxInt(left, right int) int {
	if left > right {
		return left
	}

	return right
}

func strconvFormatFloat(value float64) string {
	return strings.TrimSpace(strings.TrimRight(strings.TrimRight(strconv.FormatFloat(value, 'f', 2, 64), "0"), "."))
}

func itoa(value int) string {
	return strconv.Itoa(value)
}
