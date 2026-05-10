package api

import "encoding/json"

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
