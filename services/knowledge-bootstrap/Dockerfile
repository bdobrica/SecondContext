FROM ghcr.io/astral-sh/uv:0.12.19 AS uv
FROM python:3.13-slim
COPY --from=uv /uv /usr/local/bin/uv
ENV UV_LINK_MODE=copy UV_COMPILE_BYTECODE=1 PYTHONUNBUFFERED=1
WORKDIR /app
COPY pyproject.toml uv.lock ./
COPY src ./src
RUN uv sync --frozen --no-dev --no-editable
COPY alembic.ini ./
COPY migrations ./migrations
RUN useradd --uid 10001 --create-home knowledge
USER knowledge
EXPOSE 8090
CMD ["/app/.venv/bin/uvicorn", "knowledge_bootstrap.app:create_app", "--factory", "--host", "0.0.0.0", "--port", "8090", "--no-access-log"]
