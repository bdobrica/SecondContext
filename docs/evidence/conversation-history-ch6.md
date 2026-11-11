# CH6 adaptive selection — 2026-11-11

Implemented optional adaptive selection, disabled by default. The selection and
rollback behavior follow [ADR 0007](../adr/0007-conversation-history.md) and the
[history contract](../contracts/conversation-history-v1.md). See the
[operator guide](../adaptive-history.md) for configuration and retry semantics.
No image was published, production flag changed, schema migrated or consumer
switched to adaptive as part of this work.

## Implementation

- Independent `HISTORY_ADAPTIVE_ENABLED` capability/control. Existing ordinary
  Chat Completions, legacy Responses, recent observation and off capture preserve
  their behavior. Memory/knowledge controls remain independent.
- `standalone-1` omits dialogue only for successful dual probabilities <=0.05.
  Threshold sweeps are reported separately for calibration and holdout. The fixed
  conservative threshold prioritizes retained context over omission coverage;
  it is not a statistically calibrated guarantee.
- Recent fallback for refusal, uncertainty, older reference, invalid/oversized
  evidence/answers, access failure, timeout and provider outage. No classifier call
  when no history fits. Canonical/source/authorization failure aborts instead.
- Adaptive reservation/selection uses existing nullable `selected_turns`: NULL
  before selection, a committed empty/nonempty array before the first generator
  attempt. Failed generation and process restart reuse the exact selection. No
  migration or transcript shadow store. Snapshot, scope, source validation and
  existing erasure/abandon/expiry cleanup still apply.
- Separate adaptive configuration binding, bounded applied-selection metrics,
  generation-attempt accounting for pinned retries, and explicit rollback without
  altering existing turn identities. Older retrieval, summaries, embeddings,
  cross-session replay and automatic transcript-to-memory extraction stay deferred.

## Live paired evaluation

Only the embedded authored corpus was sent upstream, using the configured provider
credential privately through process environment. No customer database, raw private
input, Telegram transcript or live generation endpoint was used. The evaluator
called OpenAI directly. Numeric evidence is in
[conversation-history-ch6-live.json](conversation-history-ch6-live.json).

The first two-second access probe timed out before corpus calls. Its usage/cost is
unknown. A second access probe (five-second deadline) succeeded. The actual 24
classifier cases used the runtime **two-second** deadline; all returned typed
answers (20 success, four uncertain). This run used `gpt-6-luna` for Decisions and
`gpt-4.1-mini` for paired generation, with independent cold cache partitions per case.
The corpus contains 12 calibration and 12 holdout cases. Each gets one classifier
call and two independent bounded generations (recent versus selected adaptive).

| Measure | Recent baseline | Adaptive, including classifier |
| --- | --- | --- |
| Authored fact/topic checks | 24/24 | 24/24 |
| Required context lost through omission | — | 0/24 cases |
| History omitted | 0 | 4/24 |
| Estimated total, excluding access probes | $0.0039184 | $0.005388175 |
| Median measured latency | 705.5 ms | 1105 ms |

Classifier median/max latency was 301/1247 ms. Reported classifier cache writes
were 7,719 tokens, with **no cache reads** in this cold comparison. All paired costs
had valid usage/rate coverage. Classifier cost was $0.002140975; adaptive generation
alone was $0.0032472. Rates reused the official documentation reviewed the same day:
Decisions input/read/write $0.10/$0.01/$0.125 per million; generation input/cached/output
$0.40/$0.10/$1.60. See [OpenAI pricing](https://developers.openai.com/api/docs/pricing)
and [Decisions guidance](https://developers.openai.com/api/docs/guides/decisions).
Account/region/tier premiums were not independently established; estimates are not
an invoice. The unpriced initial timeout is not silently counted as free.

The two omitted long-history cases saved about **40–41%** of estimated combined
cost individually, while short standalone calls cost more. Across the full authored
mix, cost rose **37.5%** and median latency rose **399.5 ms**. These measurements do
**not** establish a general efficiency improvement or justify enabling all traffic.
CH5's warm-cache results are separate evidence; no warm adaptive savings are claimed
from this run. Thresholds 0.01/0.05 omitted four cases; 0.1/0.2 omitted five, with no
observed missed dependence in either split. This small sample does not establish
that a looser threshold is safe across production traffic, so 0.05 remains fixed.

All 48 synthetic answers were inspected from a private, exclusively created review
file; generated prose is not checked into the repository. Exact code/name/time,
second-option and correction responses retained the required facts. Prepared
compatibility handoff answers stayed on the current supplied relationship contact;
new-topic references resolved to the latest topic. Missing older restaurant/code
answers remained unknown rather than inventing a value or leaking the classifier's
older evidence into generation. One drafting case uses creative prose and checks
only the requested spelling preference; it is not a factual scene-description test.
No general astrological accuracy, production quality, adversarial robustness or
private Telegram pass is asserted from these authored examples.

## Verification

- Focused Go tests passed; root `make verify` passed formatting, vet, unit and
  mandatory real Postgres/Qdrant integration (144 top-level PASS entries, no skips).
- History/Decisions race tests and decisions/config/evaluator race unit tests passed.
  The final aggregate run also includes generation-retry metric accounting.
- Integration assertions cover both generation endpoints, standalone omission,
  uncertain/older/error fallbacks, empty history, capability/configuration drift,
  durable failed-generation selection across restart, exact saved retries, explicit
  finalization with adaptive disabled, recent rollback, source correction, session
  erasure, purge and a canonical fence injected during classification.
- The consumer's real-gateway suite passed **27 tests**, including explicit adaptive
  HTTP topic switching/follow-up, rollback and purge. Oria's normal adapter remains
  recent. Public schema/fixture synchronization passed; no generated artifact changed.

- `make test-docker` passed on the final selection implementation, including the
  packaged evaluator, schema 5, adaptive capability enable/disable, local receipt
  recovery, capture rollback, purge and dirty-schema startup rejection.

- Oria root `make verify` passed on its final code: Ruff format/lint, strict mypy
  (82 source files), 14 release checks, 611 unit (two existing upstream warnings),
  219 integration, 392 contract and 13 end-to-end checks. No infrastructure workaround
  or production maintenance was required. All task-owned test projects cleaned up
  their disposable resources through scoped scripts.

## Remaining release gates

Establish useful aggregate economics on the intended workload and continue quality
review before broad enablement. Land/publish the provider before consumer tests that
require this implementation. A future Oria adaptive opt-in must bind the selected
mode in durable prepared work, capability-check it, and drain or abandon outstanding
adaptive generation before provider rollback. Current Oria source and deployment
continue to request recent. Private Telegram review and coordinated publication/
rollout remain separate; no task result here closes them.
