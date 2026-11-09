package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"
)

// ChatClient preserves the Chat Completions wire representation. Generate remains
// the narrower text-only interface used by Responses and extraction services.
type ChatClient interface {
	Chat(context.Context, ChatRequest, func(json.RawMessage) error) (ChatResult, error)
}

type ChatRequest struct {
	Model        string
	Body         map[string]json.RawMessage
	Stream       bool
	IncludeUsage bool
}

type ChatResult struct {
	Body  json.RawMessage
	Usage Usage
}

func chatError(code string) error {
	return &Error{StatusCode: http.StatusBadGateway, Message: "upstream chat completion failed", Type: "upstream_error", Code: code}
}

func (c *OpenAIClient) Chat(ctx context.Context, request ChatRequest, emit func(json.RawMessage) error) (ChatResult, error) {
	if c.apiKey == "" {
		return ChatResult{}, chatError("missing_api_key")
	}
	body, err := json.Marshal(request.Body)
	if err != nil {
		return ChatResult{}, chatError("invalid_request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ChatResult{}, chatError("invalid_request")
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	if request.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	client := *c.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return ChatResult{}, chatError("transport_error")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Do not forward provider bodies, credentials, redirects or endpoint details.
		status := http.StatusBadGateway
		kind := "upstream_error"
		if resp.StatusCode == 400 || resp.StatusCode == 422 {
			status = 400
			kind = "invalid_request_error"
		}
		if resp.StatusCode == 429 {
			status = 429
		}
		return ChatResult{}, &Error{StatusCode: status, Message: "upstream rejected chat completion", Type: kind, Code: "upstream_rejected"}
	}
	if request.Stream {
		media, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if media != "text/event-stream" {
			return ChatResult{}, chatError("invalid_stream")
		}
		return readChatStream(resp.Body, request.IncludeUsage, emit)
	}
	payload, err := readLimitedResponse(resp.Body)
	if err != nil {
		return ChatResult{}, err
	}
	var envelope chatEnvelope
	if json.Unmarshal(payload, &envelope) != nil || !envelope.valid(false) || len(envelope.Choices) != 1 || envelope.Choices[0].Index != 0 || envelope.Choices[0].FinishReason == nil || !validFinish(*envelope.Choices[0].FinishReason) || !validAssistant(envelope.Choices[0].Message) || (envelope.Usage != nil && !envelope.Usage.valid()) {
		return ChatResult{}, chatError("invalid_completion")
	}
	result := ChatResult{Body: payload}
	if envelope.Usage != nil {
		result.Usage = envelope.Usage.internal()
	}
	return result, nil
}

type chatUsage struct {
	Prompt     int `json:"prompt_tokens"`
	Completion int `json:"completion_tokens"`
	Total      int `json:"total_tokens"`
}

func (u chatUsage) internal() Usage {
	return Usage{InputTokens: u.Prompt, OutputTokens: u.Completion, TotalTokens: u.Total}
}
func (u chatUsage) valid() bool { return u.Prompt >= 0 && u.Completion >= 0 && u.Total >= 0 }

type chatEnvelope struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Model   string `json:"model"`
	Created int64  `json:"created"`
	Choices []struct {
		Index        int             `json:"index"`
		Message      json.RawMessage `json:"message"`
		Delta        json.RawMessage `json:"delta"`
		FinishReason *string         `json:"finish_reason"`
	} `json:"choices"`
	Usage *chatUsage      `json:"usage"`
	Error json.RawMessage `json:"error"`
}

func (e chatEnvelope) valid(stream bool) bool {
	object := "chat.completion"
	if stream {
		object += ".chunk"
	}
	return e.ID != "" && e.Object == object && e.Model != "" && e.Created > 0 && len(e.Error) == 0 && e.Choices != nil
}
func validFinish(s string) bool {
	return s == "stop" || s == "length" || s == "tool_calls" || s == "content_filter"
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string  `json:"name"`
		Arguments *string `json:"arguments"`
	} `json:"function"`
}

func validAssistant(raw json.RawMessage) bool {
	var m struct {
		Role      string          `json:"role"`
		Content   json.RawMessage `json:"content"`
		ToolCalls []chatToolCall  `json:"tool_calls"`
		Refusal   *string         `json:"refusal"`
	}
	if json.Unmarshal(raw, &m) != nil || m.Role != "assistant" || len(m.Content) == 0 {
		return false
	}
	var content *string
	if json.Unmarshal(m.Content, &content) != nil {
		return false
	}
	ids := map[string]bool{}
	for _, call := range m.ToolCalls {
		if call.ID == "" || ids[call.ID] || call.Type != "function" || call.Function.Name == "" || call.Function.Arguments == nil {
			return false
		}
		ids[call.ID] = true
	}
	return true
}

type chatDelta struct {
	Role      *string `json:"role"`
	Content   *string `json:"content"`
	Refusal   *string `json:"refusal"`
	ToolCalls []struct {
		Index    *int    `json:"index"`
		ID       *string `json:"id"`
		Type     *string `json:"type"`
		Function struct {
			Name      *string `json:"name"`
			Arguments *string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}

// Bound both individual events and total bytes. Emit only validated JSON events;
// [DONE] is emitted by the HTTP handler only after a terminal choice and optional
// usage chunk. EOF, cancellation and malformed streams never become success.
func readChatStream(reader io.Reader, includeUsage bool, emit func(json.RawMessage) error) (ChatResult, error) {
	scanner := bufio.NewScanner(io.LimitReader(reader, MaxOpenAIResponseBodyBytes+1))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var data []string
	total := 0
	eventBytes := 0
	toolIDs := map[int]string{}
	toolNames := map[int]string{}
	id, model := "", ""
	var created int64
	finished, usageSeen := false, false
	result := ChatResult{}
	dispatch := func() (bool, error) {
		if len(data) == 0 {
			return false, nil
		}
		payload := strings.Join(data, "\n")
		data = nil
		eventBytes = 0
		if payload == "[DONE]" {
			if !finished || (includeUsage && !usageSeen) {
				return false, chatError("incomplete_stream")
			}
			return true, nil
		}
		var e chatEnvelope
		if json.Unmarshal([]byte(payload), &e) != nil || !e.valid(true) {
			return false, chatError("invalid_stream")
		}
		if id == "" {
			id, model, created = e.ID, e.Model, e.Created
		}
		if e.ID != id || e.Model != model || e.Created != created || usageSeen {
			return false, chatError("invalid_stream")
		}
		if len(e.Choices) == 0 {
			if !finished || !includeUsage || e.Usage == nil || !e.Usage.valid() {
				return false, chatError("invalid_stream")
			}
			usageSeen = true
			result.Usage = e.Usage.internal()
		} else {
			if len(e.Choices) != 1 || e.Choices[0].Index != 0 || finished || e.Usage != nil {
				return false, chatError("invalid_stream")
			}
			var delta *chatDelta
			if json.Unmarshal(e.Choices[0].Delta, &delta) != nil || delta == nil || (delta.Role != nil && *delta.Role != "assistant") {
				return false, chatError("invalid_stream")
			}
			for _, call := range delta.ToolCalls {
				if call.Index == nil || *call.Index < 0 || *call.Index >= 128 || (call.Type != nil && *call.Type != "function") {
					return false, chatError("invalid_stream")
				}
				index := *call.Index
				if call.ID != nil {
					if *call.ID == "" || (toolIDs[index] != "" && toolIDs[index] != *call.ID) {
						return false, chatError("invalid_stream")
					}
					for other, id := range toolIDs {
						if other != index && id == *call.ID {
							return false, chatError("invalid_stream")
						}
					}
					toolIDs[index] = *call.ID
				}
				if call.Function.Name != nil {
					toolNames[index] += *call.Function.Name
				}
			}
			if e.Choices[0].FinishReason != nil {
				if !validFinish(*e.Choices[0].FinishReason) {
					return false, chatError("invalid_stream")
				}
				if *e.Choices[0].FinishReason == "tool_calls" {
					if len(toolIDs) == 0 || len(toolIDs) != len(toolNames) {
						return false, chatError("invalid_stream")
					}
					for index, id := range toolIDs {
						if id == "" || toolNames[index] == "" {
							return false, chatError("invalid_stream")
						}
					}
				}
				finished = true
			}
		}
		if err := emit(json.RawMessage(payload)); err != nil {
			return false, err
		}
		return false, nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		total += len(line) + 1
		if total > MaxOpenAIResponseBodyBytes {
			return result, chatError("response_too_large")
		}
		if line == "" {
			done, err := dispatch()
			if err != nil || done {
				return result, err
			}
		} else if strings.HasPrefix(line, "data:") {
			eventBytes += len(line) + 1
			if eventBytes > 1<<20 {
				return result, chatError("invalid_stream")
			}
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		} else if strings.HasPrefix(line, ":") {
			// SSE comments/keepalives are not completion chunks.
		} else {
			return result, chatError("invalid_stream")
		}
	}
	if scanner.Err() != nil {
		return result, chatError("invalid_stream")
	}
	return result, chatError("incomplete_stream")
}
