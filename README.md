# SalienceGraph

A context-augmented LLM assistant prototype.

SalienceGraph is an MVP for building a persistent cognitive context layer around an LLM. Instead of relying only on a stateless chat history, it stores and retrieves structured memories about events, people, topics, beliefs, and interaction outcomes, then uses those memories to improve future responses.

The project explores whether an LLM can behave more like a situated expert when it has access to:

- episodic memory;
- hybrid semantic and lexical retrieval;
- salience scoring;
- person/topic models;
- belief and claim tracking;
- social-role context;
- goal-conditioned scenario generation;
- post-interaction feedback loops.

The first version intentionally avoids custom predictive models such as LightGBM. The MVP uses LLM-based extraction, transparent scoring rules, Qdrant for retrieval, and Postgres for canonical structured state.

## Why this exists

Modern LLMs have broad general knowledge, but they lack the evolving context that humans accumulate from daily experience.

A person makes decisions using more than facts. They use context such as:

- what happened recently;
- what seemed important;
- what changed their beliefs;
- what is useful for current work;
- who is involved;
- how those people usually respond;
- what goal the interaction is trying to achieve.

SalienceGraph is an experiment in making that context explicit, persistent, inspectable, and useful inside a chat interface.

## MVP hypothesis

> A chat interface augmented with structured memory, hybrid retrieval, and person/topic models can produce more useful answers and interaction strategies than a stateless LLM, especially for recurring work, stakeholder communication, and decision support.

The MVP should prove that the system can:

1. remember relevant prior information;
2. retrieve it based on the current goal;
3. use it to improve an answer;
4. generate better communication strategies;
5. update its internal context after an interaction;
6. expose enough debug information to understand what happened.

## Example use case

User:

> Help me ask Alex to review the infrastructure proposal.

The assistant retrieves context such as:

- Alex is competent on infrastructure topics;
- Alex dislikes vague requests;
- Alex has limited capacity this week;
- previous requests worked better when the scope was narrow;
- Dana is the approver and wants quantified risk.

The assistant can then generate:

- a recommended message;
- alternative strategies;
- likely response scenarios;
- risks;
- fallback options;
- suggested follow-up behavior.

After the interaction, the user can report what happened:

> Alex replied quickly and agreed to review, but asked me to narrow the request to the API section only.

The system then updates its memories and person/topic model so future recommendations improve.

## Architecture

```text
┌──────────────────────────┐
│ Chat client / OpenAI SDK │
└────────────┬─────────────┘
             │ OpenAI-compatible /v1/responses
             ▼
┌──────────────────────────┐
│ Go API Gateway            │
│ - auth                    │
│ - conversation state      │
│ - LLM calls               │
│ - retrieval policy        │
│ - scoring/reranking       │
│ - memory updates          │
└───────┬─────────┬────────┘
        │         │
        │         ▼
        │   ┌──────────────────┐
        │   │ Postgres          │
        │   │ - sessions        │
        │   │ - messages        │
        │   │ - memories        │
        │   │ - people          │
        │   │ - topics          │
        │   │ - beliefs         │
        │   │ - graph edges     │
        │   │ - outcomes        │
        │   └──────────────────┘
        │
        ▼
┌──────────────────────────┐
│ Qdrant                    │
│ - dense vectors           │
│ - sparse/BM25 vectors     │
│ - payload metadata        │
│ - hybrid retrieval        │
└──────────────────────────┘
        │
        ▼
┌──────────────────────────┐
│ Upstream LLM Provider     │
│ - extraction              │
│ - embeddings              │
│ - answer generation       │
│ - scenario simulation     │
│ - memory consolidation    │
└──────────────────────────┘
```

## Core loop

```text
observe -> extract -> store -> retrieve -> reason -> act -> update
```

The system observes user input or manually ingested notes, extracts structured memory, stores it, retrieves relevant context for future tasks, reasons with the LLM, and updates its memory based on outcomes.

## Planned stack

- **Language:** Go
- **API:** OpenAI-compatible `/v1/responses` endpoint
- **Vector database:** Qdrant
- **Structured storage:** Postgres
- **Embeddings:** API-based at first
- **LLM:** OpenAI-compatible provider
- **Deployment:** Docker Compose
- **License:** Apache License 2.0

## Main concepts

### Memory items

A memory item is an observed or inferred piece of context.

Examples:

- “Alex prefers narrow review scopes for infrastructure proposals.”
- “The migration project risk appears higher than originally estimated.”
- “Dana prefers quantified arguments.”
- “The user read an article about vector search tradeoffs.”

Each memory can include:

- raw text;
- summary;
- type;
- source;
- timestamp;
- people;
- topics;
- importance score;
- utility score;
- belief-impact score;
- confidence score;
- expiry or decay behavior.

### Person/topic models

The project models people at topic level, not only globally.

For example, a person may be highly competent and responsive on infrastructure topics, but unavailable or less useful on product strategy topics.

Tracked attributes may include:

- niceness;
- readiness;
- competence;
- capacity;
- confidence;
- evidence count;
- last observed timestamp.

These are uncertain, editable working estimates, not fixed judgments.

### Belief tracking

The system can track claims or assumptions that matter to the user.

Example:

```json
{
  "claim": "The migration project is more risky than originally estimated.",
  "topic": "migration",
  "stance": "supported",
  "confidence": 0.71
}
```

### Goal-conditioned retrieval

The same memory may be relevant or irrelevant depending on the current goal.

Example goals:

- get approval;
- request feedback;
- challenge an assumption;
- prepare for a meeting;
- summarize a topic;
- draft a message;
- decide between options.

### Scenario generation

For communication tasks, the assistant can generate multiple strategies and estimate likely outcomes.

Example strategies:

- direct request;
- deferential request;
- high-context request;
- low-friction scoped request.

The assistant recommends the strategy closest to the user's goal while considering risk and social context.

## Repository structure

Proposed structure:

```text
.
├── cmd/
│   └── api/
├── internal/
│   ├── api/
│   ├── config/
│   ├── db/
│   ├── debug/
│   ├── llm/
│   ├── memory/
│   ├── models/
│   ├── prompts/
│   ├── qdrant/
│   ├── retrieval/
│   └── scoring/
├── migrations/
├── deploy/
│   └── docker-compose.yml
├── docs/
│   ├── PLAN.md
│   └── TODO.md
├── README.md
└── LICENSE
```

## Planned API surface

OpenAI-compatible endpoints:

```text
GET  /v1/models
POST /v1/responses
POST /v1/chat/completions   optional
```

Internal/debug endpoints:

```text
POST /memory/ingest
POST /memory/search
POST /interactions/outcome
GET  /debug/context
GET  /debug/memory/:id
GET  /debug/person/:id
```

## Example request

```json
{
  "model": "saliencegraph-1",
  "input": "Help me ask Alex to review the infrastructure proposal.",
  "metadata": {
    "goal": "get_review",
    "people": ["Alex"],
    "project": "infrastructure proposal",
    "memory_mode": "social_strategy"
  }
}
```

## Development status

This project is currently at MVP planning stage.

See:

- [`docs/PLAN.md`](docs/PLAN.md) for the architecture and product plan.
- [`docs/TODO.md`](docs/TODO.md) for the implementation work breakdown.

## Non-goals for the MVP

The MVP does not attempt to:

- train a custom ML model;
- implement LightGBM;
- infer hidden psychological traits with high confidence;
- ingest every possible data source;
- become a full CRM;
- become a general autonomous agent;
- support multi-user enterprise permissions;
- provide production-grade compliance from day one;
- perfectly model people.

The MVP should remain narrow, inspectable, and easy to debug.

## Privacy and safety principles

Because this project may store sensitive information about people, work, and beliefs, the system should be designed with caution.

Principles:

- store evidence, not just conclusions;
- track confidence;
- distinguish facts from interpretations;
- allow editing and deletion;
- avoid irreversible judgments;
- avoid sensitive classifications;
- expire volatile observations;
- keep person models private by default;
- expose debug information to the user.

The assistant should not present uncertain social inferences as facts.

## Running locally

Local development is expected to use Docker Compose.

A future version will support:

```bash
cp .env.example .env
docker compose up --build
```

Expected local services:

- API server;
- Postgres;
- Qdrant.

## Configuration

Expected environment variables:

```bash
APP_ENV=development
HTTP_ADDR=:8080

DATABASE_URL=postgres://app:app@localhost:5432/app?sslmode=disable
QDRANT_URL=http://localhost:6333

OPENAI_API_KEY=your_api_key_here
OPENAI_BASE_URL=https://api.openai.com/v1
DEFAULT_MODEL=gpt-4.1
DEFAULT_EMBEDDING_MODEL=text-embedding-3-small
```

Variable names may change as the implementation evolves.

## License

Licensed under the Apache License, Version 2.0.

See [`LICENSE`](LICENSE).

## Author

Created by **Bogdan Dobrica**.

## Project principle

Keep the system simple, explicit, and inspectable.

The first version should prove the loop:

```text
observe -> extract -> store -> retrieve -> reason -> act -> update
```

Do not optimize too early. Do not add custom predictive models until there is enough outcome data to justify them.
