# CH7 recent-history release

The next gateway version is **0.2.3**, coordinated with Oria application
**0.2.4** and unchanged astrology MCP **0.2.1**.
See [repeat-release evidence](evidence/conversation-history-ch7-repeat.md) for CH4–CH6 publication and rollout. Version metadata does not establish image publication or
live deployment. [Release evidence](evidence/conversation-history-ch7.md) records
executed gates and remaining operational work. The authoritative rollout sequence
is Oria's [coordinated checklist](https://github.com/bdobrica/OriaEngine/blob/main/docs/conversation-history-release.md).
No automatic deployment is triggered by this document.

## Supported baseline

Use migration **000005**, scoped `conversation-history-1` and `linked-context-1`,
with ordinary [Chat Completions](contracts/chat-completions-v1.md) and existing
Responses clients unchanged. Managed `off`/`recent`, generated/explicit acceptance,
local prepared exchanges and terminal session/subject fences are implemented.
Legacy drafts are not backfilled. No schema or wire-shape change is introduced by
this release preparation.

The deployed baseline has no Decisions observation or adaptive selection. CH4/CH5
are now implemented in source and disabled by default; see the optional observation
gate below. Advertise/request adaptive only under the [CH6 gates](adaptive-history.md). Provider generation/embedding
credentials and Decisions configuration
remain solely in SecondContext. External `KNOWLEDGE_ENABLED` stays disabled in the
Oria deployment; linked projections use the independent local owning-service lane.

Before enabling capture, choose `HISTORY_RECENT_EXCHANGES`,
`HISTORY_PROMPT_INPUT_TOKENS` and `HISTORY_RESERVED_OUTPUT_TOKENS` within the configured
model's supported limits. Verify provider support for the bounded generation
request. See [history operations](conversation-history.md) for the conservative
whole-request budget, retention, concurrency and incomplete-generation behavior.

## Verification and publication

Run focused history checks, root `make verify`, dependency-backed API history race
tests, `make test-docker`, and Oria `make verify` plus `make test-history` against
this checkout. Supply `SECOND_CONTEXT_CHAT_SDK_PYTHON` to the existing SDK gate when
verifying unchanged OpenAI clients. No real provider or Telegram calls occur in
these tests. The image test exercises migration 5 and its required tables, actual
HTTP capabilities/local acceptance, restart with capture disabled, receipt recovery,
new-capture rejection, purge and delayed retry fencing. Dirty migrations must
still prevent API startup.

Publish through the version-triggered workflow, wait for verification and publication
for the exact commit, inspect source/version labels and select immutable Quay
digests. Confirm both platforms. No unpublished tag may replace a live image.
The packaging smoke image is disposable evidence, never a live release reference.

For `oria-live-test`, Oria's combined Compose file uses
`SECOND_CONTEXT_PROVIDER_HISTORY_ENABLED` for this service's `HISTORY_ENABLED`, and
`SECOND_CONTEXT_HISTORY_ENABLED` for Oria's own capture. By contrast this repository's
standalone deployment fragment uses `SECOND_CONTEXT_HISTORY_ENABLED` for provider
capture. Both defaults are false. They are distinct deployment entry points;
do not conflate the two controls when preparing or rolling back the combined stack.

Back up each owner's storage, preserve identities and encryption keys, drain callers
before provider migration/restart, and use only owning migration commands. Deploy
consistent Oria disclosure **2026-11-10.1** in ingress/workers and require fresh
acceptance. Enable provider capture and verify authenticated capabilities and actual
model continuity/erasure with fresh synthetic subjects before enabling Oria capture.
The Oria history release script uses only public HTTP, validates random-code recall
from local and accepted replacement exchanges, and purges its synthetic subject.
Its private cleanup journal permits recovery after a failed or interrupted check.
It neither sends Telegram messages nor inspects this service's database. Private
Telegram continuity/deletion requires an explicitly participating test user.

## Rollback

Keep migration 000005 and this compatible code. The down migration intentionally
refuses to drop terminal fences; never roll back to a pre-CH3 gateway or an Oria
version that cannot recover accepted work. Disable Oria capture first while keeping
the provider available. Let undispatched local acceptance work finish before also
disabling provider capture: a new `accept_local` has no preexisting remote draft.
Already committed local receipts can be retried with capture off.

`HISTORY_ENABLED=false` disables ready discovery/new managed capture while preserving
generated draft acceptance, existing receipt retries, abandonment, selective cleanup
and subject purge. Keep durable delivery and erasure workers active. Cancel rather
than regenerate uncommitted prepared inputs. Re-enabling does not resurrect canceled
or expired work, undo deletion fences, or promote legacy dialogue. The [CH6 adaptive implementation](adaptive-history.md) adds tested adaptive-to-recent
rollback, independently of this deployed baseline.

Live publication, migration, model quality, Telegram and rollback results must be
recorded separately from synthetic checks. Restrict all live maintenance to
`oria-live-test`; never prune unrelated resources on the shared host.

## Optional observation after the recent baseline

CH4/CH5 [observation and cache diagnostics](decisions-observation.md) are now
implemented in source, disabled by default. They do not change public capabilities
or automatically enable CH6 adaptive routing. Publish/verify a compatible provider digest, run
its synthetic access/economics check, and retain the consumer consent/disclosure
gates before any production enablement. The 0.2.2 deployed baseline does not
contain this observer. Disabling observation alone preserves recent replay and
all pending-turn/finalization/erasure recovery.

CH6 now implements opt-in selection and paired evaluation. Publish the provider before
consumer tests that require it. Aggregate synthetic cost/latency regressed; keep Oria
on recent until a workload-specific enablement decision and consumer implementation.
See [CH6 evidence](evidence/conversation-history-ch6.md).
