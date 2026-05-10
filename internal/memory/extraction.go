package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/llm"
	"github.com/bdobrica/SecondContext/internal/prompts"
)

var jsonFencePattern = regexp.MustCompile("(?s)^```(?:json)?\\s*(.*?)\\s*```$")

var allowedMemoryTypes = map[string]struct{}{
	"person_preference":       {},
	"person_observation":      {},
	"interaction_observation": {},
	"topic_fact":              {},
	"project_fact":            {},
	"belief_update":           {},
	"outcome":                 {},
}

var allowedEntityTypes = map[string]struct{}{
	"person":       {},
	"topic":        {},
	"project":      {},
	"organization": {},
	"document":     {},
	"event":        {},
	"claim":        {},
	"system":       {},
}

type ExtractParams struct {
	RawText     string
	Source      string
	Metadata    map[string]any
	RequestUser string
	Meta        RequestMetadata
}

type ExtractedEntity struct {
	Type       string  `json:"type"`
	Name       string  `json:"name"`
	Confidence float64 `json:"confidence"`
}

type Extraction struct {
	Summary       string            `json:"summary"`
	Type          string            `json:"type"`
	People        []string          `json:"people"`
	Topics        []string          `json:"topics"`
	Entities      []ExtractedEntity `json:"entities"`
	Importance    float64           `json:"importance"`
	Utility       float64           `json:"utility"`
	BeliefImpact  float64           `json:"belief_impact"`
	Confidence    float64           `json:"confidence"`
	ExpiresInDays *int              `json:"expires_in_days"`
}

type ExtractResult struct {
	Memory     Record
	Extraction Extraction
}

func (s *Service) ExtractAndIngest(ctx context.Context, params ExtractParams) (ExtractResult, error) {
	if s.pool == nil {
		return ExtractResult{}, &Error{StatusCode: http.StatusInternalServerError, Message: "postgres is not configured", Type: "server_error", Code: "postgres_disabled"}
	}
	if strings.TrimSpace(params.RawText) == "" {
		return ExtractResult{}, &Error{StatusCode: http.StatusBadRequest, Message: "raw_text is required", Type: "invalid_request_error", Code: "missing_raw_text", Param: "raw_text"}
	}

	extraction, err := s.extractStructuredMemory(ctx, strings.TrimSpace(params.RawText))
	if err != nil {
		return ExtractResult{}, err
	}

	record, err := s.Ingest(ctx, IngestParams{
		RawText:       strings.TrimSpace(params.RawText),
		Summary:       extraction.Summary,
		MemoryType:    extraction.Type,
		Source:        extractedSource(params.Source),
		People:        extraction.People,
		Topics:        extraction.Topics,
		Importance:    floatPointer(extraction.Importance),
		Utility:       floatPointer(extraction.Utility),
		BeliefImpact:  floatPointer(extraction.BeliefImpact),
		Confidence:    floatPointer(extraction.Confidence),
		ExpiresInDays: extraction.ExpiresInDays,
		Metadata:      params.Metadata,
		RequestUser:   params.RequestUser,
		Meta:          params.Meta,
	})
	if err != nil {
		return ExtractResult{}, err
	}

	if err := s.storeExtractedEntities(ctx, record.ID, extraction.Entities); err != nil {
		_ = s.Delete(ctx, record.ID)
		return ExtractResult{}, err
	}

	return ExtractResult{Memory: record, Extraction: extraction}, nil
}

func (s *Service) extractStructuredMemory(ctx context.Context, rawText string) (Extraction, error) {
	firstPass, err := s.llm.Generate(ctx, llm.GenerateRequest{
		Model: s.cfg.OpenAI.ChatModel,
		Messages: []llm.Message{
			{Role: "system", Content: prompts.MemoryExtractionSystemPrompt()},
			{Role: "user", Content: prompts.MemoryExtractionUserPrompt(rawText)},
		},
	})
	if err != nil {
		return Extraction{}, &Error{StatusCode: http.StatusBadGateway, Message: "failed to extract memory structure", Type: "server_error", Code: "extraction_failed"}
	}

	extraction, parseErr := parseAndValidateExtraction(firstPass.OutputText)
	if parseErr == nil {
		return extraction, nil
	}

	repair, err := s.llm.Generate(ctx, llm.GenerateRequest{
		Model: s.cfg.OpenAI.ChatModel,
		Messages: []llm.Message{
			{Role: "system", Content: prompts.MemoryExtractionRepairSystemPrompt()},
			{Role: "user", Content: prompts.MemoryExtractionRepairUserPrompt(rawText, firstPass.OutputText)},
		},
	})
	if err != nil {
		return Extraction{}, &Error{StatusCode: http.StatusBadGateway, Message: "failed to repair extraction output", Type: "server_error", Code: "extraction_repair_failed"}
	}

	extraction, parseErr = parseAndValidateExtraction(repair.OutputText)
	if parseErr != nil {
		return Extraction{}, &Error{StatusCode: http.StatusBadGateway, Message: "llm extraction output was invalid after repair", Type: "server_error", Code: "invalid_extraction_output"}
	}

	return extraction, nil
}

func parseAndValidateExtraction(raw string) (Extraction, error) {
	cleaned := strings.TrimSpace(raw)
	if matches := jsonFencePattern.FindStringSubmatch(cleaned); len(matches) == 2 {
		cleaned = strings.TrimSpace(matches[1])
	}

	decoder := json.NewDecoder(strings.NewReader(cleaned))
	decoder.DisallowUnknownFields()

	var extraction Extraction
	if err := decoder.Decode(&extraction); err != nil {
		return Extraction{}, err
	}

	if decoder.More() {
		return Extraction{}, fmt.Errorf("extra trailing data")
	}

	extraction.Summary = strings.TrimSpace(extraction.Summary)
	extraction.Type = strings.TrimSpace(extraction.Type)
	extraction.People = uniqueValues(extraction.People)
	extraction.Topics = uniqueValues(extraction.Topics)

	if extraction.Summary == "" {
		return Extraction{}, fmt.Errorf("summary is required")
	}
	if _, ok := allowedMemoryTypes[extraction.Type]; !ok {
		return Extraction{}, fmt.Errorf("unsupported memory type %q", extraction.Type)
	}

	extraction.Importance = clampScore(floatPointer(extraction.Importance))
	extraction.Utility = clampScore(floatPointer(extraction.Utility))
	extraction.BeliefImpact = clampScore(floatPointer(extraction.BeliefImpact))
	extraction.Confidence = clampScore(floatPointer(extraction.Confidence))

	if extraction.ExpiresInDays != nil && *extraction.ExpiresInDays <= 0 {
		extraction.ExpiresInDays = nil
	}

	normalizedEntities := make([]ExtractedEntity, 0, len(extraction.Entities))
	seen := make(map[string]struct{})
	for _, entity := range extraction.Entities {
		entity.Type = strings.TrimSpace(entity.Type)
		entity.Name = strings.TrimSpace(entity.Name)
		entity.Confidence = clampScore(floatPointer(entity.Confidence))

		if entity.Name == "" || entity.Type == "" {
			continue
		}
		if _, ok := allowedEntityTypes[entity.Type]; !ok {
			return Extraction{}, fmt.Errorf("unsupported entity type %q", entity.Type)
		}

		key := entity.Type + ":" + strings.ToLower(entity.Name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		normalizedEntities = append(normalizedEntities, entity)
	}
	extraction.Entities = normalizedEntities

	return extraction, nil
}

func (s *Service) storeExtractedEntities(ctx context.Context, memoryID string, entities []ExtractedEntity) error {
	if len(entities) == 0 {
		return nil
	}

	repo := db.NewMemoryEntityRepository(s.pool)
	for _, entity := range entities {
		metadata := map[string]any{"source": "llm_extraction"}
		payload, err := json.Marshal(metadata)
		if err != nil {
			return err
		}

		if _, err := repo.Create(ctx, db.CreateMemoryEntityParams{
			MemoryItemID: memoryID,
			EntityType:   entity.Type,
			EntityName:   entity.Name,
			Confidence:   entity.Confidence,
			Metadata:     payload,
		}); err != nil {
			return err
		}
	}

	return nil
}

func extractedSource(source string) string {
	trimmed := strings.TrimSpace(source)
	if trimmed == "" {
		return "llm_extract"
	}

	return trimmed
}

func floatPointer(value float64) *float64 {
	return &value
}

var _ = bytes.MinRead
