# Stage 14 Demo

Stage 14 packages the MVP into a repeatable end-to-end demo.

The demo centers on a familiar communication task:

- Alex is the reviewer.
- Alex is strong on API review, but has low capacity and prefers tightly scoped asks.
- Dana is the approver and wants quantified risk framing.

The demo runner seeds those starting conditions, asks the assistant for help, shows the retrieved context, generates strategies, records an outcome, and asks a follow-up question to show that the new outcome changes later retrieval.

## Prerequisites

The command needs the same backing services as the application:

- Postgres
- Qdrant
- an OpenAI-compatible upstream for embeddings and completions

Typical local setup:

```bash
cp .env.example .env
docker compose up -d postgres qdrant
make migrate-up
```

If you want the demo command to start its own temporary dev server, that is enough.

If you want it to target an already running API instead, start the API separately and provide `SECOND_CONTEXT_BASE_URL`.

## Run It

Embedded server mode:

```bash
make demo-stage14
```

Existing API mode:

```bash
SECOND_CONTEXT_BASE_URL=http://localhost:8080 make demo-stage14
```

## What It Does

The command executes the following flow:

1. Creates an isolated demo user for the current run.
2. Seeds initial memory items through `POST /memory/ingest`.
3. Seeds deterministic person/topic models and belief records directly in Postgres.
4. Requests a stateless draft with `disable_memory=true`.
5. Requests a memory-augmented draft for the same task.
6. Calls `POST /memory/search` to show retrieved memories and scores.
7. Requests scenario generation through `POST /v1/responses` with `memory_mode=scenario_generation`.
8. Inspects `GET /debug/context` for the scenario session.
9. Reports an outcome through `POST /interactions/outcome`.
10. Inspects `GET /debug/context` again to show the resulting updates.
11. Asks a follow-up question and inspects its retrieved context.

## Expected Result

The useful signal in the output is:

- the memory-augmented draft should be more specific than the stateless draft;
- the retrieved memories should emphasize narrow scope, Alex's low capacity, and Dana's quantified-risk preference;
- the scenario step should recommend a low-friction, API-scoped ask;
- the outcome step should create a new outcome memory;
- the follow-up retrieval should include the newly recorded outcome or its effects.

The command prints the generated user and session IDs so you can inspect the same records later on a long-running dev server.