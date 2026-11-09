# CH2 — supplied-history Chat Completions verification

Date: 2026-11-09. Scope: [implemented compatibility subset](../contracts/chat-completions-v1.md)
under ADR 0007. Source implementation only; no deployment or capability enablement.

## Verified

- Focused `go test -short ./internal/api ./internal/llm`: passed.
- `make verify`: passed formatting, vet, unit and the mandatory Postgres/Qdrant
  integration lane, with no integration skips. This includes unchanged Responses,
  model discovery, linked-context erasure and subject purge regression coverage.
- `go test -race -short ./internal/api ./internal/llm`: passed.
- Unmodified Python OpenAI SDK **3.48.0** via `TestChatOpenAISDK`: passed supplied
  multi-turn messages, separate chats for one subject, function invocation/results,
  text and tool SSE, final usage, structured output, standard error handling and
  unchanged model discovery. No custom transport or client patch was used; the
  synthetic gateway/provider ran locally with real isolated Postgres.
- New integration checks passed real memory retrieval/isolation with Postgres and
  Qdrant, independent reference controls, service-route authorization, foreign
  session rejection, subject deletion fences, incremental delivery before upstream
  completion, downstream cancellation and incomplete/malformed SSE behavior.
- Request tests cover the parameter matrix, content roles/parts, parallel tool
  result ordering, duplicate/unresolved call IDs, unsupported fields/modalities,
  header parsing, ambiguous JSON, input limits and scope conflicts. Upstream tests
  cover byte-preserved completion payloads, options, usage, tool fragments, finish
  markers, malformed/oversize bodies, errors, cancellation and redirect rejection.

Commands used Go from `/usr/local/go/bin` with
`GOCACHE=/tmp/secondcontext-go-cache`; Go was not on the default shell PATH.
Local HTTP test servers require running outside the filesystem/network sandbox.
The first full run exposed an incomplete synthetic SDK provider: it did not serve
embedding requests. The harness was corrected; the final complete gate passed.

The full gate used only the dedicated disposable Compose project
`oria-ch2-secondcontext-integration`, Postgres port 55452 and Qdrant port 56353,
with the repository's pinned `postgres:16.13-alpine` and `qdrant/qdrant:v1.15.5`
images. The test script removed that project's containers/network after each run.
No production storage, shared project or live Oria deployment was changed.

Reproduce the SDK-inclusive gate after installing `openai==3.48.0` in a separate
Python environment (the SDK is not a gateway dependency):

```sh
GOCACHE=/tmp/secondcontext-go-cache \
COMPOSE_PROJECT_NAME=oria-ch2-secondcontext-integration \
INTEGRATION_POSTGRES_PORT=55452 INTEGRATION_QDRANT_PORT=56353 \
SECOND_CONTEXT_CHAT_SDK_PYTHON=/path/to/test-venv/bin/python \
make verify
```

## Limits

No paid/live model calls, model-quality assessment, provider-specific feature
certification, Decisions/cache measurements, Docker image publication, live
Telegram checks or deployment were performed. No new runtime dependency or
migration was introduced. External `KNOWLEDGE_ENABLED` remains disabled in runtime
configuration; reference-provider tests use synthetic isolated configuration.

Ordinary Chat Completions uses caller-supplied history and does not archive it.
It does not solve Oria's current one-message Responses flow by itself. Managed
capture/replay, capability/finalize routes, acceptance/recovery/erasure state and
Oria consumer integration remain CH3/OCH2–OCH5 work. The accepted history schema
and its consumer copy are unchanged. `PLAN.md` remains an uncommitted local
execution record; public TODO and reference docs are updated with this slice.
