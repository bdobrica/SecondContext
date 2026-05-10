package api
package api

import "encoding/json"

const defaultPublicModel = "context-agent-1"

type createResponseRequest struct {
	Model        string         `json:"model"`
	Input        json.RawMessage `json:"input"`
	Instructions string         `json:"instructions,omitempty"`
	Stream       bool           `json:"stream,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	User         string         `json:"user,omitempty"`
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
	ID         string                `json:"id"`
	Object     string                `json:"object"`
	CreatedAt  int64                 `json:"created_at"`
	Status     string                `json:"status"`
	Model      string                `json:"model"`
	Output     []responseOutputItem  `json:"output"`
	OutputText string                `json:"output_text"`
	Usage      *responseUsage        `json:"usage,omitempty"`
	Metadata   map[string]any        `json:"metadata,omitempty"`
}

type responseOutputItem struct {
	ID      string                  `json:"id"`
	Type    string                  `json:"type"`
	Role    string                  `json:"role"`
	Content []responseContentBlock  `json:"content"`
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
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
	Param   string `json:"param,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

type requestMetadata struct {
	SessionID      string
	UserExternalID string
	UserName       string
	UserEmail      string
	SessionTitle   string
}
