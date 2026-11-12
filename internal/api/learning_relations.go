package api

import (
	"context"
	"encoding/json"
	"time"

	memsvc "github.com/bdobrica/SecondContext/internal/memory"
	"github.com/bdobrica/SecondContext/internal/retrieval"
	"github.com/jackc/pgx/v5"
)

type plannedObservationRelations struct {
	Candidates []memsvc.ObservationCandidate
	Relations  []memsvc.ObservationRelation
}

func (s *Server) planObservationRelations(ctx context.Context, c learningCapture, observations []memsvc.Observation) ([]plannedObservationRelations, error) {
	plans := make([]plannedObservationRelations, len(observations))
	for i, o := range observations {
		results, err := retrieval.NewService(s.cfg, s.dbPool, s.llm).Search(ctx, retrieval.SearchParams{Query: o.Summary, ResolvedUserID: c.User, UserExternalID: c.Subject, IncludeExpired: true, IncludeHistorical: true, Limit: 24})
		if err != nil {
			return nil, err
		}
		for _, r := range results {
			meta := retrieval.MetadataMap(r.Memory.Metadata)
			if r.Memory.Source != "conversation" || meta["capture_id"] == c.ID || meta["working_state"] == "superseded" {
				continue
			}
			candidate := memsvc.CandidateFromMemory(r.Memory)
			observed, err := time.Parse(time.RFC3339Nano, candidate.ObservedAt)
			if err != nil || !observed.Before(c.Observed) {
				continue
			}
			// A late retry of an old report must not undo a newer explicit edit.
			if corrected, ok := meta["corrected_at"].(string); ok {
				if at, e := time.Parse(time.RFC3339Nano, corrected); e != nil || !at.Before(c.Observed) {
					continue
				}
			}
			plans[i].Candidates = append(plans[i].Candidates, candidate)
			if len(plans[i].Candidates) == 8 {
				break
			}
		}
		plans[i].Relations, err = memsvc.RelateObservation(ctx, s.llm, s.cfg.OpenAI.ChatModel, o, c.Observed, plans[i].Candidates)
		if err != nil {
			return nil, err
		}
	}
	return plans, nil
}

// Publication uses the connection holding the subject fence. The version check
// drops decisions about an edited/deleted candidate rather than resurrecting it.
func publishObservationRelations(ctx context.Context, tx pgx.Tx, user, newer string, plan plannedObservationRelations) error {
	snapshots := map[string]memsvc.ObservationCandidate{}
	for _, c := range plan.Candidates {
		snapshots[c.ID] = c
	}
	for _, r := range plan.Relations {
		candidate := snapshots[r.ID]
		_, err := tx.Exec(ctx, `INSERT INTO observation_relations(user_id,prior_id,newer_id,kind,prior_quote,new_quote,reason)
   SELECT $1,p.id,n.id,$4,$5,$6,$7 FROM memory_items p JOIN learning_captures pc ON pc.id=p.capture_id
   JOIN memory_items n ON n.id=$3 AND n.user_id=$1
   WHERE p.id=$2 AND p.user_id=$1 AND p.updated_at=$8 AND p.qdrant_status='completed'
   AND (pc.status='completed' OR p.metadata ? 'corrected_at')
   AND NOT EXISTS(SELECT 1 FROM sessions s JOIN context_session_fences f ON f.user_id=s.user_id AND f.session_external_id=s.external_id WHERE s.id=pc.session_id)
   ON CONFLICT DO NOTHING`, user, r.ID, newer, r.Kind, r.PriorQuote, r.NewQuote, r.Reason, candidate.UpdatedAt)
		if err != nil {
			return err
		}
	}
	return nil
}

func observationWorkingContext(metadata json.RawMessage) (string, []string) {
	var meta struct {
		State     string `json:"working_state"`
		Relations []struct {
			Prior string `json:"prior_id"`
			Newer string `json:"newer_id"`
			Kind  string `json:"kind"`
		} `json:"relations"`
	}
	_ = json.Unmarshal(metadata, &meta)
	var links []string
	for _, r := range meta.Relations {
		if len(links) >= 8 {
			break
		}
		links = append(links, r.Kind+": "+r.Prior+" -> "+r.Newer)
	}
	return meta.State, links
}
