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

echo "==> starting the published-image Compose stack against fresh storage"
"${compose[@]}" up -d --wait --wait-timeout 120
version=$("${compose[@]}" run --rm --no-deps secondcontext-migrate /app/migrate version)
[[ "$version" == "version=4 dirty=false" ]]

schema=$("${compose[@]}" exec -T secondcontext-postgres psql -U secondcontext -d secondcontext -Atc \
	"SELECT to_regclass('memory_items') IS NOT NULL AND to_regclass('interaction_outcomes') IS NOT NULL AND to_regclass('subject_purges') IS NOT NULL AND to_regclass('context_projections') IS NOT NULL AND to_regclass('context_projection_operations') IS NOT NULL AND to_regclass('context_projection_sessions') IS NOT NULL")
[[ "$schema" == "t" ]]
"${compose[@]}" exec -T secondcontext wget -q -O - http://127.0.0.1:8080/healthz

echo "==> checking the Oria service credential against a migrated endpoint"
subject=oria:00000000-0000-4000-8000-000000000001
response=$("${compose[@]}" exec -T secondcontext wget -S -q -O - \
	--header="Authorization: Bearer $SECOND_CONTEXT_SERVICE_TOKEN" \
	--header="X-SecondContext-Subject: $subject" \
	--header='Content-Type: application/json' --post-data="{\"user\":\"$subject\"}" \
	http://127.0.0.1:8080/v1/subjects/purge 2>&1)
[[ "$response" == *"HTTP/1.1 200"* && "$response" == *'"status":"completed"'* ]]

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
