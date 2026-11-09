package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/bdobrica/SecondContext/internal/llm"
)

type chatRequestError struct{ code, param string }

func (e *chatRequestError) Error() string { return e.code }
func invalidChat(param string) *chatRequestError {
	return &chatRequestError{"invalid_parameter", param}
}
func unsupportedChat(param string) *chatRequestError {
	return &chatRequestError{"unsupported_feature", param}
}

type chatInput struct {
	upstream llm.ChatRequest
	context  createResponseRequest
	messages []json.RawMessage
	text     []llm.Message
}

// Options remain raw JSON after validation so absence, null, zero, whitespace in
// strings, tool schemas and supported nested fields survive the adapter unchanged.
func parseChatRequest(body map[string]json.RawMessage) (chatInput, *chatRequestError) {
	out := chatInput{upstream: llm.ChatRequest{Body: make(map[string]json.RawMessage)}}
	if body == nil {
		return out, invalidChat("")
	}
	for key, raw := range body {
		switch key {
		case "model":
			if !chatString(raw) || strings.TrimSpace(stringValue(raw)) == "" {
				return out, invalidChat(key)
			}
			out.upstream.Model = stringValue(raw)
		case "messages":
			if len(raw) > maxResponseInputBytes || json.Unmarshal(raw, &out.messages) != nil || len(out.messages) == 0 {
				return out, invalidChat(key)
			}
		case "stream":
			if !chatBool(raw) {
				return out, invalidChat(key)
			}
			_ = json.Unmarshal(raw, &out.upstream.Stream)
		case "n":
			if !chatNumber(raw, 1, 1, true) {
				return out, unsupportedChat(key)
			}
		case "temperature":
			if !isNull(raw) && !chatNumber(raw, 0, 2, false) {
				return out, invalidChat(key)
			}
		case "top_p":
			if !isNull(raw) && !chatNumber(raw, 0, 1, false) {
				return out, invalidChat(key)
			}
		case "presence_penalty", "frequency_penalty":
			if !isNull(raw) && !chatNumber(raw, -2, 2, false) {
				return out, invalidChat(key)
			}
		case "max_tokens", "max_completion_tokens":
			if !isNull(raw) && !chatNumber(raw, 1, 2147483647, true) {
				return out, invalidChat(key)
			}
		case "seed":
			if !isNull(raw) && !chatNumber(raw, -9007199254740991, 9007199254740991, true) {
				return out, invalidChat(key)
			}
		case "logprobs", "parallel_tool_calls":
			if !isNull(raw) && !chatBool(raw) {
				return out, invalidChat(key)
			}
		case "top_logprobs":
			if !isNull(raw) && !chatNumber(raw, 0, 20, true) {
				return out, invalidChat(key)
			}
		case "logit_bias":
			if !isNull(raw) {
				var biases map[string]json.RawMessage
				if json.Unmarshal(raw, &biases) != nil || biases == nil {
					return out, invalidChat(key)
				}
				for token, bias := range biases {
					if !regexp.MustCompile(`^[0-9]{1,10}$`).MatchString(token) || !chatNumber(bias, -100, 100, false) {
						return out, invalidChat(key)
					}
				}
			}
		case "stop":
			if !isNull(raw) && !chatString(raw) {
				var stops []json.RawMessage
				if json.Unmarshal(raw, &stops) != nil || len(stops) < 1 || len(stops) > 4 {
					return out, invalidChat(key)
				}
				for _, stop := range stops {
					if !chatString(stop) {
						return out, invalidChat(key)
					}
				}
			}
		case "tools":
			if !isNull(raw) {
				if e := validateChatTools(raw); e != nil {
					return out, e
				}
			}
		case "tool_choice":
			if !isNull(raw) {
				if chatString(raw) {
					v := stringValue(raw)
					if v != "auto" && v != "none" && v != "required" {
						return out, unsupportedChat(key)
					}
				} else {
					choice, e := chatObject(raw, key, "type", "function")
					if e != nil {
						return out, e
					}
					if stringValue(choice["type"]) != "function" {
						return out, unsupportedChat(key)
					}
					function, e := chatObject(choice["function"], key, "name")
					if e != nil {
						return out, e
					}
					if !chatName(function["name"]) {
						return out, invalidChat(key)
					}
				}
			}
		case "response_format":
			if !isNull(raw) {
				if e := validateChatFormat(raw); e != nil {
					return out, e
				}
			}
		case "stream_options":
			if !isNull(raw) {
				options, e := chatObject(raw, key, "include_usage", "include_obfuscation")
				if e != nil {
					return out, e
				}
				for _, v := range options {
					if !chatBool(v) {
						return out, invalidChat(key)
					}
				}
				_ = json.Unmarshal(options["include_usage"], &out.upstream.IncludeUsage)
			}
		case "store":
			if !isNull(raw) && (!chatBool(raw) || string(raw) != "false") {
				return out, unsupportedChat(key)
			}
		case "user":
			if !chatString(raw) {
				return out, invalidChat(key)
			}
			out.context.User = stringValue(raw)
			continue
		case "metadata":
			if len(raw) > maxRequestMetadataBytes {
				return out, invalidChat(key)
			}
			if !isNull(raw) {
				if e := validateChatMetadata(raw, &out.context); e != nil {
					return out, e
				}
			}
			continue
		case "disable_memory", "disable_knowledge":
			if !chatBool(raw) {
				return out, invalidChat(key)
			}
			if key == "disable_memory" {
				_ = json.Unmarshal(raw, &out.context.DisableMemory)
			} else {
				_ = json.Unmarshal(raw, &out.context.DisableKnowledge)
			}
			continue
		case "knowledge_filters":
			_, e := chatObject(raw, key, "source_ids", "document_ids", "formats")
			if e != nil {
				return out, e
			}
			if json.Unmarshal(raw, &out.context.KnowledgeFilters) != nil || !out.context.KnowledgeFilters.Valid() {
				return out, invalidChat(key)
			}
			continue
		default:
			return out, unsupportedChat(key)
		}
		out.upstream.Body[key] = raw
	}
	if out.upstream.Model == "" {
		return out, invalidChat("model")
	}
	if len(out.messages) == 0 {
		return out, invalidChat("messages")
	}
	if raw, ok := body["stream_options"]; ok && !isNull(raw) && !out.upstream.Stream {
		return out, invalidChat("stream_options")
	}
	if a, b := body["max_tokens"], body["max_completion_tokens"]; len(a) > 0 && !isNull(a) && len(b) > 0 && !isNull(b) {
		return out, invalidChat("max_completion_tokens")
	}
	pending := map[string]bool{}
	used := map[string]bool{}
	for i, raw := range out.messages {
		param := fmt.Sprintf("messages[%d]", i)
		message, e := chatObject(raw, param, "role", "content", "name", "tool_call_id", "tool_calls", "refusal")
		if e != nil {
			return out, e
		}
		role := stringValue(message["role"])
		switch role {
		case "system", "developer", "user", "assistant", "tool":
		default:
			return out, unsupportedChat(param + ".role")
		}
		if len(pending) > 0 && role != "tool" {
			return out, invalidChat(param + ".role")
		}
		if v, ok := message["name"]; ok && (!chatName(v) || role == "tool") {
			return out, invalidChat(param + ".name")
		}
		if _, ok := message["refusal"]; ok && role != "assistant" {
			return out, invalidChat(param + ".refusal")
		}
		if v, ok := message["refusal"]; ok && !isNull(v) && !chatString(v) {
			return out, invalidChat(param + ".refusal")
		}
		if v, ok := message["tool_call_id"]; ok {
			if role != "tool" || !chatString(v) || !pending[stringValue(v)] {
				return out, invalidChat(param + ".tool_call_id")
			}
			delete(pending, stringValue(v))
		} else if role == "tool" {
			return out, invalidChat(param + ".tool_call_id")
		}
		calls := message["tool_calls"]
		if len(calls) > 0 && !isNull(calls) {
			if role != "assistant" {
				return out, invalidChat(param + ".tool_calls")
			}
			var items []json.RawMessage
			if json.Unmarshal(calls, &items) != nil || len(items) == 0 || len(items) > 128 {
				return out, invalidChat(param + ".tool_calls")
			}
			for _, item := range items {
				call, e := chatObject(item, param+".tool_calls", "id", "type", "function")
				if e != nil {
					return out, e
				}
				if stringValue(call["type"]) != "function" {
					return out, unsupportedChat(param + ".tool_calls")
				}
				id := stringValue(call["id"])
				if id == "" || used[id] {
					return out, invalidChat(param + ".tool_calls")
				}
				fn, e := chatObject(call["function"], param+".tool_calls", "name", "arguments")
				if e != nil {
					return out, e
				}
				if !chatName(fn["name"]) || !chatString(fn["arguments"]) {
					return out, invalidChat(param + ".tool_calls")
				}
				pending[id] = true
				used[id] = true
			}
		}
		content, e := chatContent(message["content"], role, param+".content")
		if e != nil {
			return out, e
		}
		if (len(message["content"]) == 0 || isNull(message["content"])) && role == "assistant" && len(pending) == 0 && (len(message["refusal"]) == 0 || isNull(message["refusal"])) {
			return out, invalidChat(param + ".content")
		}
		out.text = append(out.text, llm.Message{Role: role, Content: content})
	}
	if len(pending) > 0 {
		return out, invalidChat("messages")
	}
	return out, nil
}

func chatContent(raw json.RawMessage, role, param string) (string, *chatRequestError) {
	if chatString(raw) {
		return stringValue(raw), nil
	}
	if (len(raw) == 0 || isNull(raw)) && role == "assistant" {
		return "", nil
	}
	var blocks []json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil || len(blocks) == 0 {
		return "", invalidChat(param)
	}
	var text strings.Builder
	for _, raw := range blocks {
		block, e := chatObject(raw, param, "type", "text")
		if e != nil {
			return "", e
		}
		if stringValue(block["type"]) != "text" {
			return "", unsupportedChat(param)
		}
		if !chatString(block["text"]) {
			return "", invalidChat(param)
		}
		text.WriteString(stringValue(block["text"]))
	}
	return text.String(), nil
}
func validateChatTools(raw json.RawMessage) *chatRequestError {
	var tools []json.RawMessage
	if json.Unmarshal(raw, &tools) != nil || len(tools) == 0 || len(tools) > 128 {
		return invalidChat("tools")
	}
	names := map[string]bool{}
	for _, item := range tools {
		tool, e := chatObject(item, "tools", "type", "function")
		if e != nil {
			return e
		}
		if stringValue(tool["type"]) != "function" {
			return unsupportedChat("tools")
		}
		fn, e := chatObject(tool["function"], "tools.function", "name", "description", "parameters", "strict")
		if e != nil {
			return e
		}
		if !chatName(fn["name"]) || names[stringValue(fn["name"])] {
			return invalidChat("tools.function.name")
		}
		names[stringValue(fn["name"])] = true
		if v, ok := fn["description"]; ok && !chatString(v) {
			return invalidChat("tools.function.description")
		}
		if v, ok := fn["strict"]; ok && !isNull(v) && !chatBool(v) {
			return invalidChat("tools.function.strict")
		}
		if v, ok := fn["parameters"]; ok && !isJSONObject(v) {
			return invalidChat("tools.function.parameters")
		}
	}
	return nil
}
func validateChatFormat(raw json.RawMessage) *chatRequestError {
	format, e := chatObject(raw, "response_format", "type", "json_schema")
	if e != nil {
		return e
	}
	kind := stringValue(format["type"])
	if kind == "text" || kind == "json_object" {
		if _, ok := format["json_schema"]; ok {
			return invalidChat("response_format")
		}
		return nil
	}
	if kind != "json_schema" {
		return unsupportedChat("response_format")
	}
	schema, e := chatObject(format["json_schema"], "response_format.json_schema", "name", "description", "schema", "strict")
	if e != nil {
		return e
	}
	if !chatName(schema["name"]) || !isJSONObject(schema["schema"]) {
		return invalidChat("response_format.json_schema")
	}
	if v, ok := schema["description"]; ok && !chatString(v) {
		return invalidChat("response_format.json_schema.description")
	}
	if v, ok := schema["strict"]; ok && !isNull(v) && !chatBool(v) {
		return invalidChat("response_format.json_schema.strict")
	}
	return nil
}
func validateChatMetadata(raw json.RawMessage, request *createResponseRequest) *chatRequestError {
	values, e := chatObject(raw, "metadata", "session_id", "user_external_id", "user_name", "user_email", "session_title", "goal", "people", "topics", "disable_memory", "disable_knowledge", "memory_mode")
	if e != nil {
		return e
	}
	for key, value := range values {
		switch key {
		case "disable_memory", "disable_knowledge":
			if !chatBool(value) {
				return invalidChat("metadata." + key)
			}
		case "people", "topics":
			var list []string
			if json.Unmarshal(value, &list) != nil || list == nil {
				return invalidChat("metadata." + key)
			}
		case "memory_mode":
			if stringValue(value) != "normal_answer" {
				return unsupportedChat("metadata.memory_mode")
			}
		default:
			if !chatString(value) {
				return invalidChat("metadata." + key)
			}
		}
	}
	_ = json.Unmarshal(raw, &request.Metadata)
	return nil
}
func chatObject(raw json.RawMessage, param string, keys ...string) (map[string]json.RawMessage, *chatRequestError) {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, invalidChat(param)
	}
	allowed := map[string]bool{}
	for _, key := range keys {
		allowed[key] = true
	}
	for key := range object {
		if !allowed[key] {
			return nil, unsupportedChat(param + "." + key)
		}
	}
	return object, nil
}
func stringValue(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}
func chatString(raw json.RawMessage) bool {
	var v string
	return !isNull(raw) && json.Unmarshal(raw, &v) == nil
}
func chatBool(raw json.RawMessage) bool {
	var v bool
	return !isNull(raw) && json.Unmarshal(raw, &v) == nil
}
func isNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }
func isJSONObject(raw json.RawMessage) bool {
	var v map[string]json.RawMessage
	return json.Unmarshal(raw, &v) == nil && v != nil
}
func chatNumber(raw json.RawMessage, min, max float64, integer bool) bool {
	var n float64
	return !isNull(raw) && json.Unmarshal(raw, &n) == nil && n >= min && n <= max && (!integer || math.Trunc(n) == n)
}

var chatNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func chatName(raw json.RawMessage) bool { return chatNamePattern.MatchString(stringValue(raw)) }

var chatSessionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var chatTurnPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func chatHistoryHeaders(r *http.Request, metadata map[string]any) (string, *chatRequestError) {
	h, err := parseHistoryHeaders(r, metadata)
	return h.Session, err
}

// Reject ambiguous JSON only on the new endpoint; legacy Responses keeps its
// published parser behavior. Decoder.Token handles escaped member names too.
func validateChatJSON(raw []byte) error {
	if !utf8.Valid(raw) {
		return fmt.Errorf("invalid unicode")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value func() error
	value = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		if delimiter == '{' {
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return fmt.Errorf("duplicate member")
				}
				seen[name] = true
				if err := value(); err != nil {
					return err
				}
			}
		} else if delimiter == '[' {
			for decoder.More() {
				if err := value(); err != nil {
					return err
				}
			}
		} else {
			return fmt.Errorf("invalid delimiter")
		}
		_, err = decoder.Token()
		return err
	}
	return value()
}
