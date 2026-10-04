.PHONY: sync run migrate check test test-integration

sync:
	uv sync --locked

run:
	uv run uvicorn knowledge_bootstrap.app:create_app --factory --host 127.0.0.1 --port 8090 --env-file .env --no-access-log

migrate:
	uv run --env-file .env alembic upgrade head

check:
	uv run ruff check .
	uv run ruff format --check .

test:
	uv run pytest -m 'not integration'

test-integration:
	uv run pytest --require-postgres
