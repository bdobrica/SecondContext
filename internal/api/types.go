package api

import (
	"encoding/json"
	"time"
)

const defaultPublicModel = "context-agent-1"

type createResponseRequest struct {
	Model        string          `json:"model"`
	Input        json.RawMessage `json:"input"`
	Instructions string          `json:"instructions,omitempty"`
	Stream       bool            `json:"stream,omitempty"`
	Metadata     map[string]any  `json:"metadata,omitempty"`
	User         string          `json:"user,omitempty"`
}

type listModelsResponse struct {
	Object string      `json:"object"`
	Data   []modelInfo `json:"data"`
}

type modelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type createResponseResult struct {
	ID         string               `json:"id"`
	Object     string               `json:"object"`
	CreatedAt  int64                `json:"created_at"`
	Status     string               `json:"status"`
	Model      string               `json:"model"`
	Output     []responseOutputItem `json:"output"`
	OutputText string               `json:"output_text"`
	Usage      *responseUsage       `json:"usage,omitempty"`
	Metadata   map[string]any       `json:"metadata,omitempty"`
}

type responseOutputItem struct {
	ID      string                 `json:"id"`
	Type    string                 `json:"type"`
	Role    string                 `json:"role"`
	Content []responseContentBlock `json:"content"`
}

type responseContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responseUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type apiErrorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Message   string `json:"message"`
	Type      string `json:"type"`
	Code      string `json:"code,omitempty"`
	Param     string `json:"param,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

type requestMetadata struct {
	SessionID      string
	UserExternalID string
	UserName       string
	UserEmail      string
	SessionTitle   string
}

type ingestMemoryRequest struct {
	RawText       string         `json:"raw_text"`
	Summary       string         `json:"summary"`
	Type          string         `json:"type"`
	Source        string         `json:"source,omitempty"`
	People        []string       `json:"people,omitempty"`
	Topics        []string       `json:"topics,omitempty"`
	Importance    *float64       `json:"importance,omitempty"`
	Utility       *float64       `json:"utility,omitempty"`
	BeliefImpact  *float64       `json:"belief_impact,omitempty"`
	Confidence    *float64       `json:"confidence,omitempty"`
	ExpiresInDays *int           `json:"expires_in_days,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	User          string         `json:"user,omitempty"`
}

type extractMemoryRequest struct {
	RawText  string         `json:"raw_text"`
	Source   string         `json:"source,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
	User     string         `json:"user,omitempty"`
}

type extractedEntityResponse struct {
	Type       string  `json:"type"`
	Name       string  `json:"name"`
	Confidence float64 `json:"confidence"`
}

type extractionResponse struct {
	Summary       string                    `json:"summary"`
	Type          string                    `json:"type"`
	People        []string                  `json:"people"`
	Topics        []string                  `json:"topics"`
	Entities      []extractedEntityResponse `json:"entities"`
	Importance    float64                   `json:"importance"`
	Utility       float64                   `json:"utility"`
	BeliefImpact  float64                   `json:"belief_impact"`
	Confidence    float64                   `json:"confidence"`
	ExpiresInDays *int                      `json:"expires_in_days,omitempty"`
}

type extractMemoryResponse struct {
	Memory     memoryResponse     `json:"memory"`
	Extraction extractionResponse `json:"extraction"`
}

type memoryResponse struct {
	ID            string         `json:"id"`
	UserID        string         `json:"user_id"`
	MemoryType    string         `json:"type"`
	Source        string         `json:"source"`
	RawText       string         `json:"raw_text"`
	Summary       string         `json:"summary"`
	People        []string       `json:"people"`
	Topics        []string       `json:"topics"`
	Importance    float64        `json:"importance"`
	Utility       float64        `json:"utility"`
	BeliefImpact  float64        `json:"belief_impact"`
	Confidence    float64        `json:"confidence"`
	QdrantPointID string         `json:"qdrant_point_id"`
	ExpiresAt     *string        `json:"expires_at,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	CreatedAt     string         `json:"created_at"`
	UpdatedAt     string         `json:"updated_at"`
}

type memoryListResponse struct {
	Data []memoryResponse `json:"data"`
}

type memoryDeleteResponse struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

type memorySearchRequest struct {
	Query               string   `json:"query"`
	Goal                string   `json:"goal,omitempty"`
	UserExternalID      string   `json:"user_external_id,omitempty"`
	MemoryType          string   `json:"memory_type,omitempty"`
	People              []string `json:"people,omitempty"`
	Topics              []string `json:"topics,omitempty"`
	ConfidenceThreshold *float64 `json:"confidence_threshold,omitempty"`
	IncludeExpired      bool     `json:"include_expired,omitempty"`
	Limit               int      `json:"limit,omitempty"`
}

type memorySearchScoresResponse struct {
	Final                       float64 `json:"final"`
	Hybrid                      float64 `json:"hybrid"`
	Dense                       float64 `json:"dense"`
	Sparse                      float64 `json:"sparse"`
	Retrieval                   float64 `json:"retrieval"`
	Recency                     float64 `json:"recency"`
	Importance                  float64 `json:"importance"`
	Utility                     float64 `json:"utility"`
	GoalRelevance               float64 `json:"goal_relevance"`
	BeliefImpact                float64 `json:"belief_impact"`
	Confidence                  float64 `json:"confidence"`
	MaxSimilarityToHigherRanked float64 `json:"max_similarity_to_higher_ranked"`
}

type memorySearchResultResponse struct {
	Rank   int                        `json:"rank"`
	Memory memoryResponse             `json:"memory"`
	Scores memorySearchScoresResponse `json:"scores"`
}

type memorySearchResponse struct {
	Data []memorySearchResultResponse `json:"data"`
}

type personTopicModelResponse struct {
	ID                string         `json:"id"`
	TopicID           string         `json:"topic_id"`
	TopicName         string         `json:"topic_name"`
	TopicAliases      []string       `json:"topic_aliases,omitempty"`
	Niceness          float64        `json:"niceness"`
	Readiness         float64        `json:"readiness"`
	Competence        float64        `json:"competence"`
	Capacity          float64        `json:"capacity"`
	Confidence        float64        `json:"confidence"`
	EvidenceCount     int            `json:"evidence_count"`
	EvidenceMemoryIDs []string       `json:"evidence_memory_ids,omitempty"`
	LastObservedAt    *string        `json:"last_observed_at,omitempty"`
	Summary           string         `json:"summary"`
	Metadata          map[string]any `json:"metadata,omitempty"`
	CreatedAt         string         `json:"created_at"`
	UpdatedAt         string         `json:"updated_at"`
}

type personDebugResponse struct {
	ID        string                     `json:"id"`
	UserID    string                     `json:"user_id"`
	Name      string                     `json:"name"`
	Aliases   []string                   `json:"aliases,omitempty"`
	Metadata  map[string]any             `json:"metadata,omitempty"`
	CreatedAt string                     `json:"created_at"`
	UpdatedAt string                     `json:"updated_at"`
	Models    []personTopicModelResponse `json:"models"`
}

type updatePersonModelRequest struct {
	TopicID           string     `json:"topic_id,omitempty"`
	TopicName         string     `json:"topic_name,omitempty"`
	TopicAliases      []string   `json:"topic_aliases,omitempty"`
	PersonAliases     []string   `json:"person_aliases,omitempty"`
	Niceness          *float64   `json:"niceness,omitempty"`
	Readiness         *float64   `json:"readiness,omitempty"`
	Competence        *float64   `json:"competence,omitempty"`
	Capacity          *float64   `json:"capacity,omitempty"`
	Confidence        *float64   `json:"confidence,omitempty"`
	EvidenceCount     *int       `json:"evidence_count,omitempty"`
	EvidenceMemoryIDs []string   `json:"evidence_memory_ids,omitempty"`
	LastObservedAt    *time.Time `json:"last_observed_at,omitempty"`
}

type beliefDebugResponse struct {
	ID                string         `json:"id"`
	UserID            string         `json:"user_id"`
	TopicID           string         `json:"topic_id,omitempty"`
	TopicName         string         `json:"topic_name,omitempty"`
	TopicAliases      []string       `json:"topic_aliases,omitempty"`
	Claim             string         `json:"claim"`
	Stance            string         `json:"stance"`
	Confidence        float64        `json:"confidence"`
	EvidenceMemoryIDs []string       `json:"evidence_memory_ids,omitempty"`
	HasContradiction  bool           `json:"has_contradiction"`
	Summary           string         `json:"summary"`
	Metadata          map[string]any `json:"metadata,omitempty"`
	LastUpdatedAt     string         `json:"last_updated_at"`
	CreatedAt         string         `json:"created_at"`
	UpdatedAt         string         `json:"updated_at"`
}

type beliefListResponse struct {
	Data []beliefDebugResponse `json:"data"`
}
