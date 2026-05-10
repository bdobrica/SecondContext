package llm

import "context"

type Client interface {
	Generate(ctx context.Context, request GenerateRequest) (GenerateResponse, error)
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type GenerateRequest struct {
	Model    string
	Messages []Message
}

type GenerateResponse struct {
	ID         string
	Model      string
	OutputText string
	Usage      Usage
}

type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

type Error struct {
	StatusCode int
	Message    string
	Type       string
	Code       string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}

	return e.Message
}
