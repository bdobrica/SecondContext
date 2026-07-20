#!/usr/bin/env bash
set -euo pipefail

project_name="${COMPOSE_PROJECT_NAME:-secondcontext-integration}"
compose_file="docker-compose.integration.yml"
started_services=0

cleanup() {
	if [ "$started_services" -eq 1 ]; then
		docker compose -p "$project_name" -f "$compose_file" down --volumes --remove-orphans
	fi
}
trap cleanup EXIT INT TERM

if [ -z "${POSTGRES_DSN:-}" ]; then
	command -v docker >/dev/null 2>&1 || {
		echo "error: Docker is required when POSTGRES_DSN is not supplied" >&2
		exit 1
	}
	echo "==> starting pinned Postgres and Qdrant integration services"
	started_services=1
	docker compose -p "$project_name" -f "$compose_file" up -d --wait
	POSTGRES_DSN="postgres://secondcontext:secondcontext@127.0.0.1:${INTEGRATION_POSTGRES_PORT:-55432}/secondcontext_integration?sslmode=disable"
	export POSTGRES_DSN
else
	echo "==> using caller-supplied POSTGRES_DSN"
fi

echo "==> applying database migrations (dependency failures are fatal)"
POSTGRES_ENABLED=true go run ./cmd/migrate up

echo "==> running all integration-capable packages with mandatory mode enabled"
SECOND_CONTEXT_INTEGRATION=1 OUTCOME_INTEGRATION=1 go test -v -count=1 ./... 2>&1 | tee "${TMPDIR:-/tmp}/secondcontext-integration.log"

if grep -E -- '--- SKIP:' "${TMPDIR:-/tmp}/secondcontext-integration.log"; then
	echo "error: mandatory integration lane skipped one or more tests" >&2
	exit 1
fi
