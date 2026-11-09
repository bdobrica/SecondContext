package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/bdobrica/SecondContext/internal/canonicaljson"
	"github.com/bdobrica/SecondContext/internal/db"
)

type historySelectors struct{ Session, Turn, History, Acceptance string }

func parseHistoryHeaders(r *http.Request, metadata map[string]any) (historySelectors, *chatRequestError) {
	h := historySelectors{History: "off", Acceptance: "generated"}
	for _, key := range []string{"Session", "Turn", "History", "Acceptance"} {
		name := "X-SecondContext-" + key
		values := r.Header.Values(name)
		if len(values) == 0 {
			continue
		}
		if len(values) != 1 {
			return h, &chatRequestError{"invalid_history_request", name}
		}
		v := strings.Trim(values[0], " \t")
		valid := false
		switch key {
		case "Session":
			valid = chatSessionPattern.MatchString(v)
			h.Session = v
		case "Turn":
			valid = chatTurnPattern.MatchString(v)
			h.Turn = v
		case "History":
			valid = v == "off" || v == "recent" || v == "adaptive"
			h.History = v
		case "Acceptance":
			valid = v == "generated" || v == "explicit"
			h.Acceptance = v
		}
		if !valid {
			return h, &chatRequestError{"invalid_history_request", name}
		}
	}
	rawSession, _ := metadata["session_id"].(string)
	if h.Session != "" && rawSession != "" && h.Session != rawSession {
		return h, &chatRequestError{"session_conflict", "metadata.session_id"}
	}
	if h.Session == "" {
		h.Session = stringFromMap(metadata, "session_id")
	}
	if h.Turn != "" {
		if rawSession != "" && rawSession != h.Session || !chatSessionPattern.MatchString(h.Session) {
			return h, &chatRequestError{"invalid_history_request", "session_id"}
		}
	} else if h.History != "off" || h.Acceptance != "generated" {
		return h, &chatRequestError{"invalid_history_request", "history"}
	}
	if h.History == "adaptive" {
		return h, unsupportedChat("history")
	}
	return h, nil
}
func historyHeaderPresent(r *http.Request) bool {
	for _, key := range []string{"Session", "Turn", "History", "Acceptance"} {
		if len(r.Header.Values("X-SecondContext-"+key)) > 0 {
			return true
		}
	}
	return false
}
func historyText(text string, max int) bool {
	return utf8.ValidString(text) && strings.TrimSpace(text) != "" && utf8.RuneCountInString(text) <= max
}
func historyHash(raw []byte) ([]byte, error) {
	b, e := canonicaljson.Canonicalize(raw)
	if e != nil {
		return nil, e
	}
	h := sha256.Sum256(b)
	return h[:], nil
}
func generationHash(raw json.RawMessage, user, endpoint string, h historySelectors) ([]byte, error) {
	b, e := json.Marshal(map[string]any{"contract_version": 1, "user": user, "session_id": h.Session, "turn_id": h.Turn, "endpoint": endpoint, "history": h.History, "acceptance": h.Acceptance, "body": raw})
	if e != nil {
		return nil, e
	}
	return historyHash(b)
}
func (s *Server) readHistoryJSON(w http.ResponseWriter, r *http.Request, limit int64) (json.RawMessage, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		s.historyError(w, r, 413, "request_too_large")
		return nil, false
	}
	if _, err = canonicaljson.Canonicalize(raw); err != nil || !isJSONObject(raw) {
		s.historyError(w, r, 400, "invalid_history_request")
		return nil, false
	}
	return raw, true
}
func decodeHistoryObject(raw []byte, out any) error {
	canonical, err := canonicaljson.Canonicalize(raw)
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(canonical))
	d.DisallowUnknownFields()
	return d.Decode(out)
}
func (s *Server) historyError(w http.ResponseWriter, r *http.Request, status int, code string) {
	s.writeAPIError(w, r, status, "conversation history request unavailable or invalid", "invalid_request_error", code, "")
}
func (s *Server) historyFailure(w http.ResponseWriter, r *http.Request, err error) {
	code, status := "history_unavailable", 503
	if e, ok := err.(db.HistoryError); ok {
		code = string(e)
		switch code {
		case "session_deleted", "turn_expired":
			status = 410
		case "session_not_found", "turn_not_found":
			status = 404
		case "context_budget_exceeded":
			status = 400
		default:
			status = 409
		}
	}
	s.historyError(w, r, status, code)
}

type historyFinalizeRequest struct {
	Version       int             `json:"contract_version"`
	User          string          `json:"user"`
	Session       string          `json:"session_id"`
	Turn          string          `json:"turn_id"`
	Action        string          `json:"action"`
	Response      string          `json:"response_id"`
	UserText      string          `json:"user_text"`
	AssistantText string          `json:"assistant_text"`
	Refs          []projectionRef `json:"knowledge_refs"`
}

func parseHistoryFinalize(raw []byte) (historyFinalizeRequest, bool) {
	var v historyFinalizeRequest
	if decodeHistoryObject(raw, &v) != nil || v.Version != 1 || !historyText(v.User, 256) || !chatSessionPattern.MatchString(v.Session) || !chatTurnPattern.MatchString(v.Turn) {
		return v, false
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	required := []string{"contract_version", "user", "session_id", "turn_id", "action"}
	switch v.Action {
	case "accept_generated":
		required = append(required, "response_id", "assistant_text")
		if !historyText(v.Response, 256) || !historyText(v.AssistantText, 16384) {
			return v, false
		}
	case "accept_local":
		required = append(required, "user_text", "assistant_text", "knowledge_refs")
		if !historyText(v.UserText, 4096) || !historyText(v.AssistantText, 16384) || v.Refs == nil || !validHistoryRefs(v.Refs) || !validHistoryRefsJSON(fields["knowledge_refs"]) {
			return v, false
		}
	case "abandon":
	default:
		return v, false
	}
	if len(fields) != len(required) {
		return v, false
	}
	for _, k := range required {
		if _, ok := fields[k]; !ok {
			return v, false
		}
	}
	return v, true
}
func validHistoryRefs(refs []projectionRef) bool {
	if len(refs) > 20 {
		return false
	}
	seen := map[projectionRef]bool{}
	for _, ref := range refs {
		if !ref.valid() || ref.Revision > 9007199254740991 || seen[ref] {
			return false
		}
		seen[ref] = true
	}
	return true
}

func validHistoryRefsJSON(raw []byte) bool {
	var refs []map[string]json.RawMessage
	if json.Unmarshal(raw, &refs) != nil || refs == nil {
		return false
	}
	for _, r := range refs {
		if len(r) != 3 || r["kind"] == nil || r["id"] == nil || r["revision"] == nil {
			return false
		}
	}
	return true
}
