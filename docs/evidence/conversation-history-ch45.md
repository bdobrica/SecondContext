# CH4/CH5 — Decisions observation and cache diagnostics

Implementation and synthetic live evaluation: **2026-11-11**. This is source
implementation evidence, not a publication or production-enable receipt. Deployed
SecondContext 0.2.2 remains unchanged. CH6 adaptive routing is not enabled or
advertised. See the [operator guide](../decisions-observation.md).

## Implemented boundary

- Optional bounded `/v1/decisions` client, separate server-side credential,
  supported model/recipe validation and explicit synthetic access probe.
- Independent recent-dependence and older-reference predicates. Named typed
  results, conservative uncertainty/refusal/error handling, no generator selection.
- Canonical, same-subject/session/snapshot accepted history, including local
  accepted exchanges and replacements. No pending/abandoned/legacy-draft promotion.
- Growing bounded sequence segments, explicit breakpoints and scoped opaque HMAC
  cache partitions. Current prepared content comes last; no padding or transcript
  shadow store. Source/deletion validation occurs before and after observation.
- Numeric cache/input/output/cost/latency/fallback diagnostics with bounded labels.
  Unknown usage/rates remain unpriced, and cache counts are not double counted.
- Embedded labeled synthetic evaluation, including the Oria reading handoff.
  The private birth answer/receipt is absent; current prepared compatibility facts
  identify the new topic. Current facts remain the consumer's responsibility.
- No new public endpoint/header/schema, dependency, migration or accepted ADR change.
  Existing Chat Completions/Responses bodies and advertised history modes remain
  unchanged. Settings default off and compatible capture rollback remains intact.

## Verification

The root `make verify` gate passed with formatting, vet, short tests and mandatory
Postgres/Qdrant integration tests. The first integration run caught a new observer
query excluding accepted local exchanges because those rows have no generation
field. The query now uses the existing accepted replay eligibility rule; regression
coverage checks local evidence explicitly. A subsequent complete gate passed.

Relevant `go test -race` history/Decisions API integration tests and short Decisions,
config and evaluator tests passed. `make test-docker` also passed, including the
packaged `/app/historyeval -help` check (no provider calls), schema/receipt
compatibility and capture rollback. Dependencies used only disposable
`oria-ch45-integration`, `oria-ch45-race` and `oria-ch45-packaging` projects; their containers, networks and
volumes were cleaned. No live storage or unrelated projects were touched.

Tests exercise successful standalone/older observations without changing the
recent generator prompt, exact result retries without repeated classifier calls,
managed `off`, refusal, malformed output, bad answer names/types/probabilities,
upstream access denial, redirects, response bounds, timeout, input bounds, unknown
usage/prices, cost arithmetic, segment growth/rotation, pending/abandoned exclusion,
whole-prompt budget omissions, canonical correction, selective/full erasure and a fault-injected fence between
classification and generation. Oria's provider-only deployment wiring is checked
separately: **21 deployment contract tests** passed, and **26 real-gateway
Oria history tests** passed, including the private-reading handoff with observation
both disabled and enabled. No classifier credentials are exposed to application
containers. The final budget-label adjustment is covered by the provider regression.
The consumer completed all root verification components: 611 unit, 219 integration,
392 contract, 13 end-to-end tests, formatting/lint/type checks and 14 release-selection
checks. Its Docker-dependent cases needed rerunning after safely removing four
verified empty disposable Oria networks; see the
[consumer evidence](https://github.com/bdobrica/OriaEngine/blob/main/docs/evidence/decisions-observation.md).
No host-wide pruning or unrelated code cleanup was performed.

## Live synthetic results

The exact client/recipe and embedded corpus were run against the configured provider
credential with **no production subject, transcript or private birth input**.
One benign access probe succeeded, then ten cases each had an initial/repeated
Decisions request and one recent-baseline chat generation. No Telegram message or
live configuration change occurred. The probe uses a five-second deadline; the
runtime default remains two seconds. Numerical evidence is retained in
[the sanitized report](conversation-history-ch45-live.json); no generated answers,
provider IDs, cache keys, endpoints or credentials are retained.

| Measurement | Result |
| --- | --- |
| Model/recipe | `gpt-6-luna` / `history-dependence-1` |
| Baseline generation | Existing `gpt-4.1-mini` |
| Confident label matches | 14 of 20 observations |
| Conservative uncertain fallbacks | 6 of 20 (three cases, each repeated) |
| Standalone suggestions that missed labeled context | 0 |
| Unnecessary recent suggestions | 2 |
| Decision latency | Median 309.5 ms; maximum 2252 ms |
| Short-evidence cache reads/writes | 0 |
| Long-evidence initial requests | 1100 write tokens each |
| Long-evidence repeated requests | 1100 read tokens each; 0 write tokens |
| Limited generated topic checks | 10 of 10 passed |
| Estimated 20-observation cost | $0.0012742 |
| Estimated ten-generation cost | $0.0022588 |
| Estimated access-probe cost | $0.0000452 |

Long follow-up classifier cost fell from $0.0001824 initially to $0.0000559 on
repeat (about 69%). Paired classifier-plus-generation estimate fell from $0.0007764
to $0.0006499 (about 16%). These are small synthetic samples and operator-rate
estimates, not invoice reconciliation or a promise of production savings. The
report uses official standard short-context rates checked on 2026-11-11;
account/regional/tier premiums were not independently established. Generation and
Decisions caches are measured independently. Observation adds classifier cost and
latency to the recent-only baseline; it does not yet reduce generation context.

The correction case was uncertain about older context; the prepared private-reading
handoff was also uncertain about older context; the unavailable older-reference
case was uncertain about recent dependence. All three retained recent history.
A cold call exceeded the runtime two-second default, so the production timeout can
produce additional conservative fallbacks. Repeated exact inputs establish cache
reuse here; production append-growing behavior is structurally tested but not
claimed as a live throughput benchmark.

The topic checks are simple relevance smoke tests, not model judging or proof of
accurate astrological interpretation. The older-reference baseline correctly has
no new retrieval authority. Broader quality review, threshold calibration, default
timeout measurement and evidence that adaptive selection preserves quality remain
CH6 work. The participating user's private Telegram retest remains a separate
CH7/OCH5 gate; these results do not mark it passed.
