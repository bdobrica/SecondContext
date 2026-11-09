# Chat Completions compatibility — v1

Implemented by CH2 under [ADR 0007](../adr/0007-conversation-history.md).
This is a supported subset of the [OpenAI Chat Completions API](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)
and [streaming events](https://developers.openai.com/api/reference/resources/chat/subresources/completions/streaming-events),
reviewed 2026-11-09. It does not advertise `conversation-history-1` readiness.

## Endpoint and identity

`POST /v1/chat/completions` accepts ordinary `model` and `messages`. Configure an
OpenAI client with the gateway base URL, a subject-bound bearer token and a model.
No session, turn, history or finalize call is required. Authentication and available
Postgres storage are required, including in development, as specified for new
endpoints in [history v1](conversation-history-v1.md). Existing stateless development
Responses calls remain available. Service credentials additionally carry their
existing `X-SecondContext-Subject`; body identity selectors must match that subject.
The complete operation, including streaming, holds the existing subject lock and
checks the durable purge fence. Service credentials gain only this POST route in
CH2, not model discovery, completion CRUD or unimplemented context endpoints.

The caller supplies the entire conversation. Original message order, roles,
content whitespace, text parts, names, tool IDs and tool argument strings survive
prompt assembly. A gateway system message precedes those messages with the shared
bounded memory/reference context and grounding instructions. Memory and reference
retrieval use the supplied user text and retain independent failure/disable behavior.
The gateway neither trims supplied history nor appends another chat's transcript.

CH2 does not archive supplied Chat Completions transcripts or generated results,
including successful streams. This avoids copying a full client transcript into
storage on every request. Retrieval may create the normal subject identity row;
it does not extract new memories from chat. Responses persistence is unchanged.
Managed capture, accepted turns, replay and durable retries belong to CH3. Ordinary
Chat Completions retries can execute the provider again.

## Model and parameter matrix

| Model selector | Behavior |
| --- | --- |
| `context-agent-1` | Resolves to configured `OPENAI_CHAT_MODEL`; required nonempty model field. |
| Explicit nonempty upstream model ID | Forwarded through the same configured provider and credentials. |
| `/v1/models` | Existing alias/configured-model discovery, unchanged; not a feature capability listing. |

Every row below applies to both model selectors and non-streaming/streaming unless
stated. The gateway validates this subset and forwards supported generation options
without substituting defaults. Actual model availability, context window, parameter
combinations and JSON Schema dialect/strictness are enforced by the upstream model.
A provider rejection remains an error, never a retry with discarded controls. The
synthetic contract suite verifies forwarding; it does not certify every upstream
model or account. Result/chunk `model` is the upstream model, not a rewritten alias.

| Feature | Supported profile |
| --- | --- |
| Messages | Nonempty array; `system`, `developer`, `user`, `assistant`, `tool`. String content or arrays of `{type:"text",text:...}`. Assistant may use null/omitted content with function calls or refusal text. |
| Names/refusals | Optional message `name` (except tool); assistant `refusal` string/null. |
| Function tools | `tools` with `type:"function"`, function `name`, optional description/parameters/strict. Up to 128 tools; parameter schemas are JSON objects and remain intact. |
| Tool history | Assistant `tool_calls` with unique IDs, function name and string arguments; matching `tool` results with `tool_call_id`. All outstanding calls must resolve before continuing. Parallel results may arrive in either order. No tool executes in the gateway. |
| Tool choice | `auto`, `none`, `required`, or named function object; optional `parallel_tool_calls` boolean/null. |
| Sampling | `temperature` 0–2; `top_p` 0–1; frequency/presence penalties −2–2; nullable. |
| Output budget | Positive integer `max_tokens` or `max_completion_tokens` (not both non-null), up to 2,147,483,647; model may impose a smaller limit. |
| Stop/seed | `stop`: string, 1–4 strings or null. `seed`: safe JSON integer or null; upstream determines reproducibility. |
| Probabilities | `logprobs` boolean/null, `top_logprobs` 0–20/null, `logit_bias` map of decimal token IDs to −100–100/null. Forwarded response logprob details are preserved. |
| Structured output | `response_format`: `text`, `json_object`, or `json_schema` with name/schema and optional description/strict. No gateway repair or coercion. |
| Choices | `n` omitted or exactly 1. Multiple choices are rejected with `unsupported_feature`. |
| Streaming | `stream` boolean; `stream_options` with `include_usage` and/or `include_obfuscation` booleans, only with streaming. Options and upstream obfuscation fields are preserved. |
| Provider storage | `store` omitted, false or null. True is unsupported. |
| Multimodal/other | Images, audio, video, files, refusal content parts, hosted/custom tools, deprecated functions/function_call, reasoning controls, prediction, service tiers and other unlisted fields are unsupported. |

Unknown top-level or structured option/message/tool keys are rejected with HTTP 400
`unsupported_feature`; malformed supported values use `invalid_parameter`. Unknown
JSON Schema keywords inside function parameters/output schemas remain provider-owned.
Tool/function/schema names use `[A-Za-z0-9_-]{1,64}`. Non-string arguments or
unresolved/foreign/duplicate call IDs are invalid. Argument strings need not contain
valid JSON: callers remain responsible for validating generated arguments.

## Local context options

These are optional extensions, consumed locally and never forwarded as provider
parameters. Ordinary SDK clients need none of them.

- `user`: must match the authenticated subject when supplied.
- `disable_memory`, `disable_knowledge`: independent booleans, default false.
- `knowledge_filters`: existing `source_ids`, `document_ids`, `formats` contract.
- `metadata`: existing local keys `session_id`, `user_external_id`, `user_name`,
  `user_email`, `session_title`, `goal`, `people`, `topics`, `disable_memory`,
  `disable_knowledge`, `memory_mode`. String/array/boolean types follow their existing
  meanings. Only `memory_mode:"normal_answer"` is supported here; scenario orchestration
  remains on Responses. Other metadata keys are explicitly unsupported, including
  arbitrary provider dashboard metadata.

`X-SecondContext-Session` and matching `metadata.session_id` can select an existing
session's ownership boundary. A foreign session returns 404 `session_not_found`.
No session selector retrieves history or creates a session in CH2. Legacy metadata-only
session syntax remains accepted. Explicit headers follow the history contract's
ASCII/whitespace/duplicate rules. `History: off` and `Acceptance: generated` are the
only supported values. Turn IDs, recent/adaptive history and explicit acceptance
return 400 `unsupported_feature`; malformed headers return `invalid_history_request`
and conflicting session selectors return `session_conflict`. `knowledge_refs` remains
on Responses; it is unsupported here until the managed lifecycle is implemented.

## Results, streaming and bounds

The completion body stays a standard `chat.completion`, preserving ID, timestamp,
choices/message/tool calls/refusal, finish reason, usage/detail fields and provider
fingerprint. Usage is preserved when provided. The supported finish reasons are
`stop`, `length`, `tool_calls`, `content_filter`.

Streams use `text/event-stream` and flushed `data: <JSON>\n\n` events. Validated
`chat.completion.chunk` events retain the same ID/model/timestamp and original
content/tool deltas, indices, finish reasons and usage. With `include_usage:true`,
completion requires the final empty-choices usage chunk. `[DONE]` is emitted only
after an upstream terminal choice and `[DONE]`, plus requested usage. Intermediate
usage is null/absent; counters record actual usage, not estimates.

A malformed or incomplete stream fails. Before the first chunk, return a bounded
JSON error (normally 502). After streaming starts, emit a standard error envelope
in a data event and close without `[DONE]`. If the downstream connection is gone or
the deadline expires, close without a success marker. Client cancellation cancels
the provider HTTP request and releases the subject lock. There is no success,
accepted-history state or persistence on an incomplete stream.

The existing 30-second request deadline and configured upstream request timeout
apply. Writes receive the request deadline so a stalled consumer cannot hold the
subject lock indefinitely. No automatic provider retries or credential-bearing
redirects are followed on this endpoint. Provider 400/422 maps to 400, 429 to 429;
other provider failures map to 502 with sanitized OpenAI-shaped errors. Provider
error bodies, prompts and credentials are not relayed or added to logs.

Requests are limited to 1 MiB, encoded messages to 256 KiB and metadata to 64 KiB.
Exactly one JSON object is required; duplicate object members and invalid UTF-8
are rejected. Non-streaming upstream bodies and total SSE input are bounded to
8 MiB, individual SSE data events to 1 MiB. Supplied messages are never silently
truncated to fit a model context window; a model may reject the complete prompt.

## Verification and SDK example

```python
from openai import OpenAI

client = OpenAI(base_url="https://gateway.example/v1", api_key=subject_token)
messages = [{"role": "user", "content": "My project is Iris."}]
reply = client.chat.completions.create(model="context-agent-1", messages=messages)
messages += [reply.choices[0].message, {"role": "user", "content": "What is its name?"}]
for chunk in client.chat.completions.create(
    model="context-agent-1", messages=messages, stream=True,
    stream_options={"include_usage": True},
):
    if chunk.choices:
        print(chunk.choices[0].delta.content or "", end="")
```

`make verify` runs formatting, vet, unit and mandatory dependency-backed integration
checks. For the additional unchanged-client check, install `openai==3.48.0` in an
isolated test virtual environment and set `SECOND_CONTEXT_CHAT_SDK_PYTHON` to its
Python executable when running `make verify`. `TestChatOpenAISDK` starts a local
synthetic provider and gateway and invokes [the client test](../../scripts/test-chat-sdk.py).
It makes no paid model calls and uses no production credentials. The SDK is a test
environment dependency, not a Go gateway dependency. Without the environment variable,
the integration log explicitly reports that the SDK check was not requested.
See [CH2 evidence](../evidence/chat-completions-ch2.md) for executed checks and limits.
