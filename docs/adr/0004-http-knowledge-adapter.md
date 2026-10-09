# ADR 0004: Optional HTTP knowledge adapter and isolated context

Status: Accepted — recorded retrospectively 2026-10-10

## Context

SecondContext can use documentary evidence without importing the Python service
or reading its stores. A knowledge outage must not prevent ordinary gateway use,
and a multi-subject gateway must not accidentally share one owner's credential.

## Decision

Define `internal/knowledge.KnowledgeProvider` and implement it with the standalone
service's `POST /v1/search`. Disable the adapter by default. Map exact resolved
SecondContext subjects to distinct service tokens in server configuration. An
unmapped subject receives no knowledge; there are no namespace wildcards, caller
tokens or fallback shared credentials.

Run memory and knowledge retrieval concurrently and merge their separately
labeled results into a context packet. Bound HTTP time, query size and response
bytes, disable redirects/proxy inheritance and validate evidence IDs/scores/page
ranges. Degrade with stable status codes rather than exposing upstream errors.
Keep gateway startup/health and the normal Compose stack independent of Python.

Give retrieved sections fixed reservations using UTF-8 byte counts as a
conservative tokenizer-independent bound. Preserve full knowledge citations while
clipping text or omitting oversized items. Persist the actual evidence, omissions
and accounting with response metadata and development debug output.

Provide independent `disable_memory` and `disable_knowledge` controls. Tell the
model that document passages are evidence, not instructions, and that disagreement
with episodic observations must remain visible. Retrieval does not create memory,
beliefs or person models.

## Consequences

Reference evidence can improve a first answer with no prior memory. Either
retrieval branch can still supply context when the other fails. Credential setup
is explicit for every subject, including service-token UUID subjects. The gateway
does not automatically consume extracted candidates or propagate subject purge to
the knowledge owner's resources.

`disable_memory` alone is a memory comparison, not a fully stateless run when
knowledge is enabled. Fixed reservations can leave unused space; the retrieved
budget is not a complete model context-window check. A prompt policy and cited
evidence do not guarantee model groundedness.

See [adapter configuration](../../README.md#reference-knowledge-adapter-k9),
[knowledge integration](../knowledge-base.md#secondcontext-integration) and
[evaluation controls](../evaluation.md).
