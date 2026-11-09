package api

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func (h *historyHarness) projection(ref projectionRef, operation string, op int) {
	h.t.Helper()
	body := projectionRequest{Version: 1, Operation: operation, OperationID: historyTurnID(op), User: h.user}
	if operation == "upsert" {
		body.Projection = &contextProjection{Record: ref, Label: "synthetic event", Summary: "linked synthetic significance", KnowledgeKind: "user_statement", Sources: []projectionRef{ref}}
	} else {
		body.Record = &ref
	}
	historyStatus(h.t, h.call("/v1/knowledge/"+operation, body, nil), 200, "")
}
func TestManagedHistoryLinkedErasureIntegration(t *testing.T) {
	h := newHistoryHarness(t)
	ctx := context.Background()
	ref := projectionRef{Kind: "event", ID: historyTurnID(501), Revision: 1}
	h.projection(ref, "upsert", 502)
	local := map[string]any{"user_text": "source-linked local input", "assistant_text": "source-linked local interpretation", "knowledge_refs": []projectionRef{ref}}
	historyStatus(t, h.finalize(1, "accept_local", local), 200, "")
	// The next request carries no new refs; inherited session deps still fence it.
	draft := h.generate(2, "recent", "explicit", "/v1/responses", "follow up without refs")
	id := historyResponseID(t, draft)
	var owner string
	if err := h.pool.QueryRow(ctx, "SELECT id::text FROM users WHERE external_id=$1", h.user).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	fn := "history_failure_" + strings.ReplaceAll(strings.TrimPrefix(h.user, "oria:"), "-", "")
	_, err := h.pool.Exec(ctx, fmt.Sprintf("CREATE FUNCTION %s() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'synthetic cleanup failure'; END; $$ LANGUAGE plpgsql; CREATE TRIGGER %s BEFORE DELETE ON sessions FOR EACH ROW WHEN (OLD.external_id='%s') EXECUTE FUNCTION %s()", fn, fn, h.session, fn))
	if err != nil {
		t.Fatal(err)
	}
	drop := func() {
		_, _ = h.pool.Exec(ctx, "DROP TRIGGER IF EXISTS "+fn+" ON sessions; DROP FUNCTION IF EXISTS "+fn+"()")
	}
	defer drop()
	gone := ref
	gone.Revision = 2
	deletion := projectionRequest{Version: 1, Operation: "delete", OperationID: historyTurnID(503), User: h.user, Record: &gone}
	historyStatus(t, h.call("/v1/knowledge/delete", deletion, nil), 503, "")
	before := h.client.count()
	h.restart()
	historyStatus(t, h.generate(2, "recent", "explicit", "/v1/responses", "follow up without refs"), 410, "session_deleted")
	historyStatus(t, h.generate(3, "off", "generated", "/v1/responses", "no replay"), 410, "session_deleted")
	historyStatus(t, h.finalize(2, "accept_generated", map[string]any{"response_id": id, "assistant_text": "late"}), 410, "session_deleted")
	historyStatus(t, h.finalize(3, "accept_local", map[string]any{"user_text": "late", "assistant_text": "late", "knowledge_refs": []projectionRef{}}), 410, "session_deleted")
	historyStatus(t, h.call("/v1/responses", map[string]any{"input": "legacy late", "disable_memory": true, "disable_knowledge": true, "metadata": map[string]any{"session_id": h.session}}, nil), 410, "session_deleted")
	if h.client.count() != before {
		t.Fatal("provider saw fenced history")
	}
	drop()
	historyStatus(t, h.call("/v1/knowledge/delete", deletion, nil), 200, "")
	for _, table := range []string{"conversation_turns", "context_projection_sessions", "messages", "sessions"} {
		var count int
		if err = h.pool.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE user_id=$1", owner).Scan(&count); err != nil || count != 0 {
			t.Fatal("payload survived erasure", table, count, err)
		}
	}
	historyStatus(t, h.generate(2, "recent", "explicit", "/v1/responses", "follow up without refs"), 410, "session_deleted")
	historyStatus(t, h.call("/v1/knowledge/delete", deletion, nil), 200, "")
	// Whole-subject purge removes session fences as well as history; its permanent
	// subject fence continues to reject delayed generation and finalization.
	historyStatus(t, h.call("/v1/subjects/purge", map[string]any{"user": h.user}, nil), 200, "")
	var fences int
	if err = h.pool.QueryRow(ctx, "SELECT count(*) FROM context_session_fences WHERE user_id=$1", owner).Scan(&fences); err != nil || fences != 0 {
		t.Fatal(fences, err)
	}
	historyStatus(t, h.finalize(2, "accept_generated", map[string]any{"response_id": id, "assistant_text": "late"}), 410, "subject_deleted")
}
func TestManagedHistoryRevisionChecksAndLocalAtomicityIntegration(t *testing.T) {
	h := newHistoryHarness(t)
	ctx := context.Background()
	first := projectionRef{Kind: "event", ID: historyTurnID(601), Revision: 1}
	second := projectionRef{Kind: "event", ID: historyTurnID(602), Revision: 1}
	h.projection(first, "upsert", 603)
	// A valid first ref followed by an absent ref must not leave a partially
	// accepted local exchange or even a source link.
	fields := map[string]any{"user_text": "input", "assistant_text": "interpretation", "knowledge_refs": []projectionRef{first, second}}
	historyStatus(t, h.finalize(1, "accept_local", fields), 409, "history_source_conflict")
	var count int
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM context_projection_sessions l JOIN users u ON u.id=l.user_id WHERE u.external_id=$1`, h.user).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial links", count, err)
	}
	// Generation reservation must obey the same atomic linkage rule: private
	// inbound bytes cannot survive a crash as an unlinked pending turn.
	body := map[string]any{"input": "source-dependent input", "disable_memory": true, "disable_knowledge": true, "knowledge_refs": []projectionRef{first, second}}
	headers := map[string]string{"X-SecondContext-Session": h.session, "X-SecondContext-Turn": historyTurnID(1)}
	historyStatus(t, h.call("/v1/responses", body, headers), 409, "history_source_conflict")
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM conversation_turns WHERE user_id=(SELECT id FROM users WHERE external_id=$1)`, h.user).Scan(&count); err != nil || count != 0 {
		t.Fatal("unlinked pending transcript", count, err)
	}
	if err := h.pool.QueryRow(ctx, `SELECT count(*) FROM context_projection_sessions WHERE user_id=(SELECT id FROM users WHERE external_id=$1)`, h.user).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial generation links", count, err)
	}
	h.projection(second, "upsert", 604)
	historyStatus(t, h.finalize(1, "accept_local", fields), 200, "")
	// Simulate interrupted/inconsistent source state without a new request ref.
	_, err := h.pool.Exec(ctx, `UPDATE context_projections SET revision=2 WHERE user_id=(SELECT id FROM users WHERE external_id=$1) AND record_id=$2`, h.user, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	historyStatus(t, h.generate(2, "recent", "generated", "/v1/responses", "no new refs"), 409, "history_source_conflict")
	if h.client.count() != 0 {
		t.Fatal("stale source reached provider")
	}
	_, err = h.pool.Exec(ctx, `UPDATE context_projections SET revision=1 WHERE user_id=(SELECT id FROM users WHERE external_id=$1) AND record_id=$2`, h.user, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	id := historyResponseID(t, h.generate(2, "recent", "explicit", "/v1/responses", "valid refs"))
	first.Revision = 2
	h.projection(first, "upsert", 605)
	historyStatus(t, h.generate(2, "recent", "explicit", "/v1/responses", "valid refs"), 410, "session_deleted")
	historyStatus(t, h.finalize(2, "accept_generated", map[string]any{"response_id": id, "assistant_text": "old reading"}), 410, "session_deleted")
	// A newly selected session can use the new canonical revision.
	h.session += "-new"
	fields["knowledge_refs"] = []projectionRef{first, second}
	historyStatus(t, h.finalize(1, "accept_local", fields), 200, "")
	historyStatus(t, h.generate(2, "recent", "generated", "/v1/responses", "new session"), 200, "")
}
