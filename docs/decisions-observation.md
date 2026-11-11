# History Decisions observation and cache measurement

CH4/CH5 implement an optional observer under [ADR 0007](adr/0007-conversation-history.md).
Observation still uses the existing recent-history prompt. [CH6 adaptive selection](adaptive-history.md)
is a separate opt-in; older replay, automatic memory extraction and changes to
deterministic salience remain deferred.
The public [history contract](contracts/conversation-history-v1.md), capability
shape and OpenAI Chat Completions response bodies are unchanged. Ordinary supplied
history, legacy Responses, managed `off`, local finalization and saved generation
retries do not invoke Decisions. Observation can be disabled independently without
changing history configuration hashes, pending-turn recovery or accepted receipts.

## Provider boundary

An enabled managed `recent` generation asks two named predicates: `recent_dependence`
and `older_reference`. The versioned recipe is `history-dependence-1`. Input consists
of bounded, accepted canonical exchanges plus current prepared messages/instructions.
Current instructions are serialized as evidence, not promoted into classifier
instructions. No memory/retrieval packet, request metadata, credentials, raw subject
ID, raw session ID or private clarification is added by the observer. The consumer
still determines what prepared input is allowed to leave its boundary. A missing
private exchange cannot be reconstructed; see the [handoff evaluation](evidence/conversation-history-ch45.md).

The subject advisory lock remains held. Canonical session dependencies and deletion
fences are checked before loading observation content, immediately before Decisions,
and again before generation. Storage/source failures abort; only classifier failures
fall back. An upstream Decisions credential/access failure falls back to recent
history, while a gateway authorization failure never reaches either provider.
Observation adds no durable transcript copy, migration, derived index or deletion API.

`POST /v1/decisions` uses a separate credential, bounded request/response bytes,
a deadline, no retries, no redirects and no environment proxy inheritance. HTTPS is
required except for local loopback tests. Answers must have both expected names in
order, predicate types and probabilities within [0,1]. Missing/wrong answers,
refusal, timeout and upstream errors preserve recent history. Values strictly
between 0.2 and 0.8 are labeled uncertain. These are diagnostic thresholds, not
calibrated permission to omit context. A confident older-reference signal records
an observation; it does not fetch older material for generation.

## Configuration

All settings and secrets belong to SecondContext. The published-image Compose
fragment maps `SECOND_CONTEXT_DECISIONS_*` and
`SECOND_CONTEXT_HISTORY_GENERATION_*` environment settings to this provider only.
Enable only after an explicit
synthetic access probe and the consumer disclosure/consent release gates. Gateway
health and model discovery alone do not establish Decisions access.

| Setting | Default | Meaning |
| --- | --- | --- |
| `DECISIONS_OBSERVE_ENABLED` | `false` | Observe new managed recent generations |
| `DECISIONS_BASE_URL` | `https://api.openai.com/v1` | Provider API root |
| `DECISIONS_API_KEY` | empty | Required when observing; no implicit generation-key fallback |
| `DECISIONS_MODEL` | `gpt-6-luna` | Only currently supported model |
| `DECISIONS_RECIPE` | `history-dependence-1` | Supported question recipe |
| `DECISIONS_CACHE_SECRET` | empty | Independent random secret, at least 32 bytes |
| `DECISIONS_TIMEOUT` | `2s` | Positive deadline, at most 5 seconds |
| `DECISIONS_MAX_INPUT_BYTES` | `65536` | Complete serialized request cap, 1024–262144 |
| `DECISIONS_SEGMENT_EXCHANGES` | `32` | Segment sequence-slot width, 1–64 |

The observer runs within the existing request deadline and holds the subject lock
for its bounded call. It does not spend the whole generation timeout retrying the
classifier. Roll back with `DECISIONS_OBSERVE_ENABLED=false`; no migration or history
reset is needed. Deployment/publication is separate from source implementation.

## Cache layout and lifecycle

The earliest recent exchange retained after whole-prompt budget fitting determines a fixed sequence-slot segment
anchor. Load accepted exchanges from that anchor through the immutable snapshot,
chronologically, with at most 128 whole exchanges. As new turns arrive, the start
stays fixed until the next segment boundary; abandoned slots are not content.
Recent selection and the generator's whole-prompt budget remain unchanged. Local
accepted exchanges use the same eligibility rule as generated accepted exchanges.
An oversized serialized observation is skipped with `input_limit`; text is never
clipped or padded to force cache reuse.

Shared input starts with stable evidence framing, then chronological JSON exchanges,
then the current prepared request. At most the last four exchange parts carry explicit
breakpoints; the current request has none. Recent sequence labels live in question
instructions outside the reusable prefix. Keys are HMAC-SHA256 over the recipe,
model, internal subject/session identities, context generation and segment anchor.
They are opaque, bounded and never emitted in routine telemetry. Replicas use the
same configured secret. Rotating the secret
changes partitions but grants no authority and does not erase provider cache entries.

Correction/erasure fences and erases the entire linked session under the existing
contract. Every later call revalidates canonical eligibility; no cached result or
old transcript is used to bypass that check. A newly validated session has a new
partition. Local deletion acknowledges only the existing local/linked coverage,
not provider cache/retention deletion.

The official [Decisions guide](https://developers.openai.com/api/docs/guides/decisions)
and [request reference](https://developers.openai.com/api/reference/resources/decisions/methods/create)
were reviewed on 2026-11-11: the reusable shared prefix needs at least 1,024 tokens;
question content does not count. Explicit mode permits up to four written
breakpoints. The default 30-minute cache lifetime is a minimum, not a deletion
promise. There is no assumption of shared caches with generation.

## Diagnostics and economics

`second_context_decisions_*` metrics use only bounded outcome, suggestion,
short/long (eight or fewer/more than eight exchanges) and cold/warm/unknown labels.
Warm means the provider actually reported a positive cache-read count, not merely
a repeated request. Counters cover requests, known usage, input/output/total tokens,
cache reads/writes, generation attempts and measured generation usage. Duration
sums and request counts allow average classifier/generation latency. They contain
no transcript, raw ID, key, provider payload or per-user label. Metrics are process
local and reset on restart, following the existing observability model.

Optional complete rate groups (USD per million tokens):

- `DECISIONS_{INPUT,CACHED,WRITE}_USD_PER_MILLION`.
- `HISTORY_GENERATION_{INPUT,CACHED,OUTPUT}_USD_PER_MILLION`.

Rates are operator-supplied estimates, not embedded pricing promises. Account,
region, model, service tier and long-context premiums must be included where
applicable. Missing usage/rates mean unknown, never free. Decisions cache reads
and writes are subsets of input. Cost is `(input-read-write)*input_rate +
read*read_rate + write*write_rate`, divided by one million. Decisions output is
currently uncharged. Chat cost additionally includes output and its own cache
reads. Invalid/absent cache accounting leaves that cost unpriced. Separate
classifier/generation estimates include measurable failed/refused attempts; paired
combined estimates increment only when both costs are known. Inspect the matching
priced/usage-known counters to avoid interpreting incomplete totals as full spend.

## Synthetic evaluation

From a configured shell, without placing secrets on the command line:

```sh
go run ./cmd/historyeval -live -check-access
go run ./cmd/historyeval -live -generate > /private/evidence/history-eval.jsonl
```

The runtime image also includes `/app/historyeval`, with the same flags and
embedded corpus, for operators without a Go installation. Run it with the
provider environment; do not pass credentials on its command line.

Without `-live` the command refuses all provider calls. It uses only the embedded
synthetic corpus, never a production database or transcript. The full run makes
one access call, two observations per ten cases and, with `-generate`, one bounded
baseline generation per case. It creates fresh cache partitions for each run,
measures initial/repeated requests, and reports actual cache usage, latency, costs,
probabilities, labels and conservative fallback counts. Access failures terminate
before the corpus. Refusals/uncertainty are measurements, not evaluator crashes.

Cases cover follow-ups, second-option references, corrections, topic switches,
accepted local exchanges, the prepared private-reading handoff, and missing/present
older references. Long cases use a substantive authored itinerary, not padding.
Generation keeps the latest eight exchanges and current prepared input regardless
of classifier results. Topic-word checks are a limited relevance smoke test;
no generated answers are printed or persisted by the tool. They do not establish
interpretation accuracy. CH6 adds a larger paired corpus and recorded answer review; see
[adaptive evaluation](evidence/conversation-history-ch6.md). The original
[CH4/CH5 results](evidence/conversation-history-ch45.md) remain historical evidence.
