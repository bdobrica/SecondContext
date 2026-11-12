package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bdobrica/SecondContext/internal/llm"
	memsvc "github.com/bdobrica/SecondContext/internal/memory"
	"github.com/bdobrica/SecondContext/internal/prompts"
)

// Commands are deliberately a small, explicit language. Only a complete latest
// user message can authorize an action; neither a model nor prior/tool text can.
// Broader paraphrased belief updates are CL4, not an unvalidated mutation parser.
type memoryCommand struct{ Action, Target, Summary string }

func parseMemoryCommand(messages []llm.Message) memoryCommand {
	if len(messages) == 0 || messages[len(messages)-1].Role != "user" {
		return memoryCommand{}
	}
	raw := strings.TrimSpace(messages[len(messages)-1].Content)
	if len(raw) > 4096 || strings.ContainsAny(raw, "\n\r") {
		return memoryCommand{}
	}
	lower := strings.ToLower(strings.TrimSuffix(raw, "."))
	for _, verb := range []string{"please ", "can you ", "could you "} {
		if strings.HasPrefix(lower, verb) {
			raw = raw[len(verb):]
			lower = lower[len(verb):]
			break
		}
	}
	switch strings.TrimRight(lower, "?!.") {
	case "pause learning", "stop learning":
		return memoryCommand{Action: "pause"}
	case "resume learning", "enable learning":
		return memoryCommand{Action: "resume"}
	case "what do you remember", "what do you remember about me", "show my memories", "show my memory":
		return memoryCommand{Action: "inspect"}
	}
	for _, prefix := range []struct{ Text, Action string }{
		{"what do you remember about ", "inspect"}, {"show memories about ", "inspect"},
		{"forget what you remember about ", "forget"}, {"forget memory ", "forget"},
		{"correct what you remember about ", "correct"}, {"correct memory ", "correct"},
	} {
		if !strings.HasPrefix(strings.ToLower(raw), prefix.Text) {
			continue
		}
		target := strings.TrimSpace(raw[len(prefix.Text):])
		summary := ""
		if prefix.Action == "correct" {
			at := strings.Index(strings.ToLower(target), " to ")
			if at < 0 {
				return memoryCommand{Action: "help"}
			}
			summary = strings.TrimSpace(target[at+4:])
			target = strings.TrimSpace(target[:at])
			if summary == "" || len(summary) > 2000 {
				return memoryCommand{Action: "help"}
			}
		} else {
			target = strings.TrimRight(target, "?.")
		}
		target = strings.Trim(target, "\"“”")
		// Deictic references are not resolved from an untrusted assistant's list.
		if target == "" || len(target) > 300 || containsFold([]string{"it", "that", "this", "them", "him", "her", "those", "these", "everything", "all"}, target) {
			return memoryCommand{Action: "help"}
		}
		return memoryCommand{Action: prefix.Action, Target: target, Summary: summary}
	}
	return memoryCommand{}
}

const memoryCommandHelp = `Use “What do you remember about Alex?”, “Correct memory <ID or matching phrase> to <new observation>”, “Forget memory <ID or matching phrase>”, “Pause learning”, or “Resume learning”. Choose a specific ID when several observations match. You can also search and edit at /memory/inspector.`
const forgettingScope = "Matching old transcripts will not recreate this observation. Your chat client and saved conversation history may still contain and supply the original dialogue; forgetting personal memory does not erase that dialogue. A genuinely new report can be learned."

func commandPacket() *prompts.ContextPacket {
	return &prompts.ContextPacket{Mode: prompts.ResponseModeNormalAnswer, InterpretationStatus: "memory_command", EvidenceStatus: "disabled"}
}

// Commands return a local receipt. No generator can fabricate mutation success.
// Explicit-acceptance managed drafts cannot mutate until the user issues an
// ordinary request; accepting an assistant draft never authorizes an operation.
func (s *Server) executeMemoryCommand(ctx context.Context, request createResponseRequest, cmd memoryCommand, explicit bool) (string, error) {
	if requestDisablesMemory(request) {
		return "Personal memory is disabled for this request. Open /memory/inspector to manage it, or send a separate request with personal memory enabled.", nil
	}
	if cmd.Action == "help" {
		return memoryCommandHelp, nil
	}
	if explicit && cmd.Action != "inspect" {
		return "This client uses explicit draft acceptance. Manage memory in /memory/inspector or send this command in an ordinary chat request; accepting a draft does not perform memory changes.", nil
	}
	if s.dbPool == nil || authenticatedSubject(ctx) == "" {
		return "Memory management requires an authenticated account with storage available.", nil
	}
	user, _, err := s.actorUser(ctx)
	if err != nil {
		return "", err
	}
	switch cmd.Action {
	case "pause", "resume":
		err = s.setLearning(ctx, user.ID, cmd.Action == "resume")
		if err != nil {
			return "", err
		}
		if cmd.Action == "pause" {
			return "Learning is paused. Unfinished captures were canceled. Existing memories remain available for recall. Use temporary chat to disable both capture and personal recall for a request.", nil
		}
		return "Learning is enabled for future conversations. Previously suppressed input stays suppressed.", nil
	}
	memories, err := s.observations(ctx, user.ID, cmd.Target, 11, 0)
	if err != nil {
		return "", err
	}
	if len(memories) == 0 {
		return "No matching conversational observation was found. Nothing changed. " + memoryCommandHelp, nil
	}
	if cmd.Action == "inspect" || len(memories) > 1 {
		var out strings.Builder
		if cmd.Action != "inspect" {
			out.WriteString("Several observations match. Nothing changed. Choose a memory ID and repeat the correction or forget request.\n")
		} else {
			out.WriteString("Stored observations (not independent verification):\n")
		}
		for i, m := range memories {
			if i == 10 {
				out.WriteString("More matches exist. Narrow the phrase or use /memory/inspector.\n")
				break
			}
			var meta map[string]any
			_ = json.Unmarshal(m.Metadata, &meta)
			fmt.Fprintf(&out, "\n%s — %s\nObserved: %s; source: %v; evidence: %v; time scope: %v", m.ID, truncateText(m.Summary, 360), firstNonEmpty(stringFromMap(meta, "observed_at"), m.CreatedAt.UTC().Format(time.RFC3339)), meta["source_reference"], meta["evidence"], meta["time_scope"])
			if m.Source == "meeting_note" {
				fmt.Fprintf(&out, "; note: %v; meeting date: %v; speaker: %v", meta["title"], meta["meeting_date"], meta["speaker"])
			}
			state, relations := observationWorkingContext(m.Metadata)
			fmt.Fprintf(&out, "; working state: %s; relations: %s", state, strings.Join(relations, "; "))
			if meta["corrected_at"] != nil {
				fmt.Fprintf(&out, "; corrected: %v", meta["corrected_at"])
			}
			if m.ExpiresAt != nil {
				fmt.Fprintf(&out, "; expires: %s", m.ExpiresAt.UTC().Format(time.RFC3339))
			}
			out.WriteString("\n")
		}
		out.WriteString("\nInspect original sources and capture/recall status at /memory/inspector.")
		return out.String(), nil
	}
	m := memories[0]
	if cmd.Action == "correct" {
		m, err = s.correctObservation(ctx, m.ID, cmd.Summary)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Corrected memory %s: %s\nThe original source, labels and expiry are retained; dependent summaries were invalidated. Future personal recall uses the correction when this observation is eligible.", m.ID, m.Summary), nil
	}
	if err = s.suppressMemoryCapture(ctx, m.ID); err != nil {
		return "", err
	}
	if err = memsvc.NewService(s.cfg, s.dbPool, s.llm).Delete(ctx, m.ID); err != nil {
		return "", err
	}
	return "Forgot memory " + m.ID + " and invalidated its dependent summaries. " + forgettingScope, nil
}

func memoryCommandCompletion(content string, request llm.ChatRequest, emit func(json.RawMessage) error) (llm.ChatResult, error) {
	id := newIdentifier("chatcmpl")
	now := time.Now().Unix()
	usage := map[string]int{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
	body, _ := json.Marshal(map[string]any{"id": id, "object": "chat.completion", "created": now, "model": request.Model, "choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": content}, "finish_reason": "stop"}}, "usage": usage})
	if request.Stream {
		for _, choice := range []any{map[string]any{"index": 0, "delta": map[string]string{"role": "assistant", "content": content}, "finish_reason": nil}, map[string]any{"index": 0, "delta": map[string]string{}, "finish_reason": "stop"}} {
			chunk, _ := json.Marshal(map[string]any{"id": id, "object": "chat.completion.chunk", "created": now, "model": request.Model, "choices": []any{choice}})
			if err := emit(chunk); err != nil {
				return llm.ChatResult{}, err
			}
		}
		if request.IncludeUsage {
			chunk, _ := json.Marshal(map[string]any{"id": id, "object": "chat.completion.chunk", "created": now, "model": request.Model, "choices": []any{}, "usage": usage})
			if err := emit(chunk); err != nil {
				return llm.ChatResult{}, err
			}
		}
	}
	return llm.ChatResult{Body: body}, nil
}

func memoryCommandTextFormat(body map[string]json.RawMessage) bool {
	if raw, ok := body["response_format"]; ok {
		var format struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &format) != nil || format.Type != "text" {
			return false
		}
	}
	if raw, ok := body["tool_choice"]; ok && string(raw) != `"none"` && string(raw) != `"auto"` {
		return false
	}
	return true
}
