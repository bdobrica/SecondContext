#!/usr/bin/env bash
set -euo pipefail

# Exercise the distributed image and Compose file without host Go, source mounts,
# model calls, published ports, or any existing application databases/volumes.
cd "$(dirname "${BASH_SOURCE[0]}")/.."
project_name="${COMPOSE_PROJECT_NAME:-secondcontext-docker-smoke-$$}"
export SECOND_CONTEXT_IMAGE="secondcontext-gateway:smoke-$$"
export SECOND_CONTEXT_POSTGRES_PASSWORD=docker-smoke-only-password
export SECOND_CONTEXT_SERVICE_NAMESPACE=oria
export SECOND_CONTEXT_SERVICE_TOKEN=docker-smoke-only-service-token
export SECOND_CONTEXT_OPENAI_API_KEY=docker-smoke-no-model-calls
export SECOND_CONTEXT_HISTORY_ENABLED=true
export SECOND_CONTEXT_HISTORY_ADAPTIVE_ENABLED=false
export SECOND_CONTEXT_DECISIONS_OBSERVE_ENABLED=false
compose=(docker compose --env-file /dev/null -p "$project_name" -f deploy/compose.secondcontext.yml)

cleanup() {
	"${compose[@]}" down --volumes --remove-orphans
	docker image rm "$SECOND_CONTEXT_IMAGE" >/dev/null 2>&1 || true
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

echo "==> building the gateway runtime image (API, migration tool and SQL)"
docker build --tag "$SECOND_CONTEXT_IMAGE" .

echo "==> checking the packaged synthetic evaluator without making provider calls"
"${compose[@]}" run --rm --no-deps secondcontext /app/historyeval -help

echo "==> starting the published-image Compose stack against fresh storage"
"${compose[@]}" up -d --wait --wait-timeout 120
version=$("${compose[@]}" run --rm --no-deps secondcontext-migrate /app/migrate version)
[[ "$version" == "version=7 dirty=false" ]]

schema=$("${compose[@]}" exec -T secondcontext-postgres psql -U secondcontext -d secondcontext -Atc \
	"SELECT to_regclass('memory_items') IS NOT NULL AND to_regclass('interaction_outcomes') IS NOT NULL AND to_regclass('subject_purges') IS NOT NULL AND to_regclass('context_projections') IS NOT NULL AND to_regclass('context_projection_operations') IS NOT NULL AND to_regclass('context_projection_sessions') IS NOT NULL AND to_regclass('conversation_turns') IS NOT NULL AND to_regclass('context_session_fences') IS NOT NULL AND to_regclass('learning_captures') IS NOT NULL AND to_regclass('learning_settings') IS NOT NULL")
[[ "$schema" == "t" ]]
"${compose[@]}" exec -T secondcontext wget -q -O - http://127.0.0.1:8080/healthz

echo "==> checking history capability and receipts in the packaged runtime"
subject=oria:00000000-0000-4000-8000-000000000001
post_history() {
    "${compose[@]}" exec -T secondcontext wget -S -q -O - \
        --header="Authorization: Bearer $SECOND_CONTEXT_SERVICE_TOKEN" \
        --header="X-SecondContext-Subject: $subject" \
        --header='Content-Type: application/json' --post-data="$2" \
        "http://127.0.0.1:8080$1" 2>&1 || true
}
capability=$(post_history /v1/context/capabilities "{\"user\":\"$subject\"}")
[[ "$capability" == *"HTTP/1.1 200"* && "$capability" == *'"implementation":"conversation-history-1"'* && "$capability" == *'"history_modes":["off","recent"]'* ]]
local_turn="{\"contract_version\":1,\"user\":\"$subject\",\"session_id\":\"packaged-history-check\",\"turn_id\":\"00000000-0000-4000-8000-000000000001\",\"action\":\"accept_local\",\"user_text\":\"Synthetic local question\",\"assistant_text\":\"Synthetic local answer\",\"knowledge_refs\":[]}"
receipt=$(post_history /v1/context/turns/finalize "$local_turn")
[[ "$receipt" == *"HTTP/1.1 200"* && "$receipt" == *'"status":"accepted"'* && "$receipt" == *'"sequence":1'* ]]

echo "==> adaptive capability has an independent packaged enable/rollback switch"
export SECOND_CONTEXT_HISTORY_ADAPTIVE_ENABLED=true
export SECOND_CONTEXT_DECISIONS_API_KEY=docker-smoke-no-decision-calls
export SECOND_CONTEXT_DECISIONS_CACHE_SECRET=docker-smoke-cache-partition-secret-only
"${compose[@]}" up -d --wait --wait-timeout 120 secondcontext
capability=$(post_history /v1/context/capabilities "{\"user\":\"$subject\"}")
[[ "$capability" == *"HTTP/1.1 200"* && "$capability" == *'"history_modes":["off","recent","adaptive"]'* ]]
export SECOND_CONTEXT_HISTORY_ADAPTIVE_ENABLED=false
"${compose[@]}" up -d --wait --wait-timeout 120 secondcontext
capability=$(post_history /v1/context/capabilities "{\"user\":\"$subject\"}")
[[ "$capability" == *"HTTP/1.1 200"* && "$capability" == *'"history_modes":["off","recent"]'* ]]

echo "==> capture rollback preserves receipts and purge on schema 7"
export SECOND_CONTEXT_HISTORY_ENABLED=false
"${compose[@]}" up -d --wait --wait-timeout 120 secondcontext
capability=$(post_history /v1/context/capabilities "{\"user\":\"$subject\"}")
[[ "$capability" == *"HTTP/1.1 503"* ]]
retry=$(post_history /v1/context/turns/finalize "$local_turn")
compact_retry="${retry//[[:space:]]/}"
[[ "$retry" == *"HTTP/1.1 200"* && "$compact_retry" == *'"status":"accepted"'* && "$compact_retry" == *'"sequence":1'* ]]
# Use another turn in the existing session: new local capture is disabled.
new_turn="${local_turn/00000000-0000-4000-8000-000000000001\",\"action/00000000-0000-4000-8000-000000000002\",\"action}"
rejected=$(post_history /v1/context/turns/finalize "$new_turn")
[[ "$rejected" == *"HTTP/1.1 503"* ]]

echo "==> checking the Oria service credential against a migrated endpoint"
response=$("${compose[@]}" exec -T secondcontext wget -S -q -O - \
	--header="Authorization: Bearer $SECOND_CONTEXT_SERVICE_TOKEN" \
	--header="X-SecondContext-Subject: $subject" \
	--header='Content-Type: application/json' --post-data="{\"user\":\"$subject\"}" \
	http://127.0.0.1:8080/v1/subjects/purge 2>&1)
[[ "$response" == *"HTTP/1.1 200"* && "$response" == *'"status":"completed"'* ]]
delayed=$(post_history /v1/context/turns/finalize "$local_turn")
[[ "$delayed" == *"HTTP/1.1 410"* ]]

echo "==> applying migrations again preserves existing state"
"${compose[@]}" run --rm --no-deps secondcontext-migrate
fence=$("${compose[@]}" exec -T secondcontext-postgres psql -U secondcontext -d secondcontext -Atc \
	"SELECT completed_at IS NOT NULL FROM subject_purges WHERE external_id = '$subject'")
[[ "$fence" == "t" ]]

echo "==> migration failure prevents API startup"
"${compose[@]}" stop secondcontext
"${compose[@]}" rm -f secondcontext secondcontext-migrate
"${compose[@]}" exec -T secondcontext-postgres psql -U secondcontext -d secondcontext -v ON_ERROR_STOP=1 -c \
	"UPDATE schema_migrations SET dirty = true"
if "${compose[@]}" up -d --wait --wait-timeout 60 secondcontext; then
	echo "error: deployment accepted a dirty migration version" >&2
	exit 1
fi
running=$("${compose[@]}" ps --status running --services secondcontext)
[[ -z "$running" ]]
echo "==> Docker packaging checks passed"
