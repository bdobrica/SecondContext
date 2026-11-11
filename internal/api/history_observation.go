package api

import (
	"net/http"
	"time"

	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/decisions"
	"github.com/bdobrica/SecondContext/internal/llm"
)

// Observation failures cannot change generation. Canonical failures are returned
// separately and must abort the request; they are never classifier fallbacks.
func (s *Server) observeHistory(r *http.Request, repo db.HistoryRepository, session db.HistorySession, turn db.HistoryTurn, selected []db.HistoryTurn, current []llm.Message, instructions string) (decisions.Result, int, error) {
	start := turn.Snapshot
	if len(selected) > 0 {
		start = selected[0].Sequence - 1
	}
	anchor := decisions.Anchor(start, s.cfg.Decisions.SegmentExchanges)
	segment, err := repo.ObservationSegment(r.Context(), session, turn, anchor)
	if err != nil {
		return decisions.Result{}, 0, err
	}
	e := decisions.Evidence{Current: struct {
		Messages     []llm.Message `json:"messages"`
		Instructions string        `json:"instructions,omitempty"`
	}{current, instructions}, RecentSequences: []int64{}}
	for _, t := range selected {
		e.RecentSequences = append(e.RecentSequences, t.Sequence)
	}
	for _, t := range segment {
		e.History = append(e.History, decisions.Exchange{Sequence: t.Sequence, User: t.UserText, Assistant: t.AssistantText})
	}
	request, err := decisions.Build(s.cfg.Decisions, decisions.Scope{Subject: session.UserID, Session: session.ID, Generation: session.Generation, Segment: anchor}, e)
	if err != nil {
		return decisions.Result{Outcome: "input_limit"}, len(segment), nil
	}
	if err = db.CheckHistorySession(r.Context(), repo.Conn, session.UserID, session.ExternalID); err != nil {
		return decisions.Result{}, 0, err
	}
	return s.decisions.Decide(r.Context(), request), len(segment), nil
}

type historyObservation struct {
	Decision            decisions.Result
	Exchanges           int
	Generation          *decisions.Usage
	GenerationDuration  time.Duration
	GenerationAttempted bool
}
