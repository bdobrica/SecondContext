# Service context and subject purge — v1

## Credentials and subject selection

`AUTH_ENABLED=true` enables authentication. Existing `AUTH_BEARER_TOKENS` entries
remain `subject=token`. Optional `AUTH_SERVICE_TOKENS` entries are comma-separated
`namespace:=token` pairs. A namespace matches `[a-z][a-z0-9_-]{0,31}`. For example,
the configured namespace `oria:` owns `oria:<canonical lowercase UUID>` subjects.
Secrets belong in runtime configuration, not source control. Duplicate credentials,
duplicate namespaces, overlapping user subjects and service/user token reuse are
startup errors; service tokens with auth disabled also fail startup.

A service request carries `Authorization: Bearer <service-token>` and exactly one
`X-SecondContext-Subject: oria:<uuid>` header. Only POST requests to these endpoints
are permitted:

- `/v1/responses`
- `/memory/ingest`
- `/v1/subjects/purge`

Other routes, missing/malformed headers or a foreign namespace return 403
`service_scope_denied`. Invalid bearer authentication returns 401. Nonempty body
`user` or `metadata.user_external_id` selectors must equal the selected subject;
conflicts remain 400 `identity_conflict`. The response metadata echoes this same
subject. No default service subject is inferred. User bearer tokens cannot delegate
using the header; their existing body-selector and resource-ownership checks remain.

Response and memory schemas are unchanged. Ordinary clients need no new header.
Service consumers must treat the namespace as permanent identity configuration;
changing it does not migrate existing subjects or globally unique session IDs.

## POST /v1/subjects/purge

Authentication is always required, even in development. A normal bearer token may
purge its own subject; a service credential may purge only the subject it selected
within its namespace. The body accepts exactly one JSON object:

```json
{"user":"oria:11111111-1111-4111-8111-111111111111"}
```

`user` is required and must equal the authenticated subject. Success is HTTP 200:

```json
{"contract_version":1,"user":"oria:11111111-1111-4111-8111-111111111111","status":"completed"}
```

Success confirms erasure of the subject's user row, sessions, messages, memories,
memory entities, people, topics, person/topic models, beliefs, graph edges and
interaction outcomes, plus all points filtered by its canonical user ID in the
configured Qdrant collection (including orphaned points). Existing cascades perform
canonical deletion. A recognized missing collection is an empty index; arbitrary
404s or incomplete acknowledgements are failures. Index upserts and purge deletes
wait for completion before the subject lock is released or SQL deletion commits.

The `subject_purges` row retains only the external subject ID, creation time and
completion time. This marker is intentionally not erased. Pending and completed
subjects return HTTP 410 `subject_deleted` on subsequent authenticated requests,
except another purge request. Ordinary repository user creation also refuses a
fenced subject. Unknown subjects receive the same durable marker and completed
response; retries never recreate user records.

PostgreSQL or Qdrant failure returns 503 (`subject_unavailable` before fencing, or
`purge_incomplete` during purge). Retry the same subject after recovery. A crash
following index deletion but before canonical commit simply repeats index deletion.
The subject remains fenced throughout. Do not treat a timeout, 404, generic 2xx or
wrong-subject acknowledgement as deletion success.

The operation runs within the existing 30-second HTTP deadline; a large purge may
need retries. HTTP cancellation does not clear the durable marker. Backups, old
index collections, external LLM retention and external application records are
outside this endpoint. The caller owns user confirmation and application deletion
orchestration; SecondContext owns erasure of its active stores.
