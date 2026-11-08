# Linked context projections — v1

Implementation identifier: `linked-context-1`. [ADR 0006](../adr/0006-linked-context-projections.md)
adds four explicit POST routes to the service credential allowlist. All require
authenticated subject selection and exact body `user`; body conflicts fail. Legacy
user tokens remain subject-bound; neither credentials nor model output can select
foreign subjects. Subject deletion fences and advisory locks cover the entire call.

The shared wire shapes are the closed projection DTOs in Oria's
`contracts/knowledge/v1.json`: `RecordRef` is kind entity/event/reading, UUID and
positive revision. `ContextProjection` includes record, label (1–160 characters),
summary (1–2048), user_statement/interpretation kind, 1–32 source refs, unique house
themes 1–12 and optional salience (finite score 0–1, user_stated/inferred basis and
method version). No arbitrary metadata/profile/raw birth fields are accepted.
Consumers prepare text: string fields alone are not semantic privacy detection.

| POST route | Body / completed result |
| --- | --- |
| `/v1/knowledge/capabilities` | `{user}` / `{contract_version:1,user,implementation:"linked-context-1",history_coverage:"linked_sessions",index:"postgres",status:"ready"}` |
| `/v1/knowledge/upsert` | `{contract_version:1,operation:"upsert",user,operation_id,projection}` / receipt below |
| `/v1/knowledge/query` | `{contract_version:1,operation:"query",user,references:[],house_themes:[],limit:10}` / `{contract_version:1,operation:"query",user,matches:[]}` |
| `/v1/knowledge/delete` | `{contract_version:1,operation:"delete",user,operation_id,record}` / receipt below |

Success is HTTP 200. Query requires references (max32) and/or house themes (max12),
limit1–20, default10. Results are owner-scoped, exclude deleted/stale dependencies,
and match requested current refs or topics. The consumer still rehydrates every
source against current authorized canonical records before use.

Receipts echo contract_version1, exact user/operation/operation_id/record,
status completed and coverage projection_and_indexes for upsert, or
active_stores_and_linked_history for deletion. Wrong or partial receipts are not
success. Unknown fields fail; HTTP400 is invalid request, 401 missing auth, 403
service scope, 409 revision/idempotency conflict, 503 unavailable/incomplete cleanup.

A subject/operation UUID binds immutable content by hash. Same retries resume or
return completion; conflicts return409. Upsert advances revision or repeats the
identical same revision. Deleted UUIDs can never be upserted again, even at a higher
revision. Deletion advances the last live revision and commits a payload-free
operation reservation and terminal fence BEFORE cleanup. A crash/SQL failure
leaves the fence; requests cannot retrieve or generate from its dependencies.
Retry with the same operation finishes cleanup; only then is completed returned.
No historical payloads are retained in receipts.

Postgres owns this lane's GIN index, summaries, refs and session links. It does not
write projection vectors, cognitive models, beliefs or legacy graphs. Selective
erasure removes dependent summaries and indexes/links, and whole response sessions
linked through knowledge_refs, including user messages, retained assistant drafts
and context packets. Correction invalidates dependent summaries and clears linked
sessions. Entire-session deletion can remove unrelated turns within a linked session.
Independent unlinked chat, backups, retired indexes and external-provider copies
remain outside this selective coverage. Full subject purge includes every new table.

`POST /v1/responses` optionally accepts `knowledge_refs`, at most twenty RecordRefs.
Authentication and a session are required when used. The service verifies exact
projection/source revisions, records source/session links before generation and
adds selected summaries as untrusted recollections, never instructions, calculated
facts or authority. Omitting the field preserves existing behavior. Caller policy,
calculated facts and accepted data-use disclosure remain application responsibilities.

Ranking `linked-salience-1` uses importance0.60, exponential recency0.15 (30-day
scale), exact link0.20, thematic overlap0.05. Default importance is neutral0.5;
latest explicit significance for a source overrides inference. Mention count is
not importance. Query examines at most2,000 recent active owner projections with
stable UUID tie-breaking. House tags are traditional topics, not measured placements.
Migration000004 and a compatible release are required before consumers enable it.
