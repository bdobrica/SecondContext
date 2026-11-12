package api

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/bdobrica/SecondContext/internal/db"
	"github.com/bdobrica/SecondContext/internal/prompts"
)

// Additive headers leave completion/tool/SSE bodies untouched. IDs refer to
// current canonical observations: inspection never retains a deleted claim.
func setContextEvidenceHeaders(w http.ResponseWriter, packet *prompts.ContextPacket) {
	if packet == nil {
		return
	}
	ids := make([]string, 0, len(packet.MemoryContext))
	for _, m := range packet.MemoryContext {
		ids = append(ids, m.ID)
	}
	w.Header().Set("X-SecondContext-Memory-Ids", strings.Join(ids, ","))
	if packet.InterpretationStatus != "" {
		w.Header().Set("X-SecondContext-Interpretation", packet.InterpretationStatus)
	}
	if packet.EvidenceStatus != "" {
		w.Header().Set("X-SecondContext-Evidence", packet.EvidenceStatus)
	}
}

var evidenceIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func (s *Server) handleContextEvidence(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ids := strings.Split(r.URL.Query().Get("ids"), ",")
	if len(ids) > responseContextMemoryLimit {
		s.writeAPIError(w, r, 400, "at most six memory IDs are supported", "invalid_request_error", "invalid_ids", "ids")
		return
	}
	for i, id := range ids {
		ids[i] = strings.ToLower(strings.TrimSpace(id))
		if !evidenceIDPattern.MatchString(ids[i]) {
			s.writeAPIError(w, r, 400, "memory UUIDs are required", "invalid_request_error", "invalid_ids", "ids")
			return
		}
	}
	owner, ok, err := s.actorUser(r.Context())
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	if !ok {
		s.writeAPIError(w, r, 401, "authentication required", "authentication_error", "authentication_required", "")
		return
	}
	memories, err := db.NewMemoryRepository(s.dbPool).ListByIDs(r.Context(), ids)
	if err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	if err = db.NewMemoryRepository(s.dbPool).AnnotateObservations(r.Context(), memories); err != nil {
		s.writeMemoryError(w, r, err)
		return
	}
	result := memoryListResponse{Data: make([]memoryResponse, 0, len(memories))}
	for _, m := range memories {
		if m.UserID != owner.ID || (m.ExpiresAt != nil && !m.ExpiresAt.After(time.Now())) {
			continue
		}
		result.Data = append(result.Data, toMemoryResponse(m))
	}
	writeJSON(w, 200, result)
}
