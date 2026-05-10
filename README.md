# SecondContext

SecondContext is a context-augmented LLM gateway for recurring work: it stores structured memory about people, topics, prior interactions, and outcomes, retrieves the most relevant context for a current request, and uses that context to produce better answers and communication strategies than a stateless model.

## Stage 0 decisions

- Primary user persona: an individual knowledge worker, technical lead, or founder who repeatedly needs help with stakeholder communication, reviews, prioritization, and decisions across ongoing projects.
- First demo use case: help the user ask Alex to review an infrastructure proposal using remembered preferences, prior outcomes, and current capacity signals.
- Supported memory types in v0: interaction observations, person preferences, project or topic facts, belief updates, and reported outcomes.
- Explicit non-goals for the MVP: custom ML, LightGBM, broad third-party ingestion, enterprise multi-user permissions, and high-confidence personality inference.
- Initial stack choices: Go 1.24, `chi`, Postgres, Qdrant, OpenAI, `text-embedding-3-small`, and `golang-migrate`.

## What is bootstrapped

- A runnable Go API service in `cmd/api`.
- Environment-based configuration loading in `internal/config`.
- Structured JSON logging with request IDs.
- A `GET /healthz` endpoint that reports app and Postgres health.
- Local infrastructure via Docker Compose for the API, Postgres, and Qdrant.
- Initial repository layout for later retrieval, memory, prompts, and debugging work.

## Quick start

1. Copy `.env.example` to `.env`.
2. Set `OPENAI_API_KEY` if you want to wire the upstream provider later.
3. Start the local stack with `docker compose up --build`.
4. Check the API with `curl http://localhost:8080/healthz`.

For a local process without Docker, start Postgres separately and run `go run ./cmd/api`.

## Repository layout

```text
cmd/api
internal/api
internal/config
internal/db
internal/debug
internal/llm
internal/memory
internal/models
internal/prompts
internal/qdrant
internal/retrieval
internal/scoring
migrations
deploy
docs
```

## Near-term implementation order

1. Add the initial Postgres schema and repositories.
2. Implement OpenAI-compatible endpoints and upstream passthrough.
3. Add manual memory ingestion and dense indexing.
4. Add structured extraction, hybrid retrieval, and reranking.
5. Add prompt augmentation, scenario generation, and outcome updates.

See `PLAN.md`, `TODO.md`, and `docs/architecture.md` for the product plan and architecture.
