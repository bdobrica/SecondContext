# Stage 15 Evaluation

Stage 15 adds a repeatable evaluation harness for comparing stateless and memory-augmented behavior.

The evaluator is designed to answer one practical question: is the system actually better than a stateless baseline on recurring, context-heavy tasks?

## What It Adds

- a dataset-driven evaluation command at `cmd/eval`
- a seeded benchmark dataset in `cmd/eval/stage15_dataset.json`
- automatic baseline vs augmented response generation
- retrieval precision and coverage tracking against manually curated expected memory keys
- optional strategy-success and outcome-feedback checks on cases that include outcomes
- a generated JSON and Markdown report
- explicit placeholders for manual ratings instead of pretending those scores can be automated faithfully

## Run It

Use the embedded dev server mode:

```bash
make eval-stage15
```

In embedded mode, the evaluator uses a fresh Stage 15 Qdrant collection for each run so old local dev collections do not contaminate retrieval metrics.

Or target a running development API:

```bash
SECOND_CONTEXT_BASE_URL=http://localhost:8080 make eval-stage15
```

Reports are written under `.artifacts/stage15/` by default. You can override that with `STAGE15_OUTPUT_DIR`.

## What The Evaluator Measures

Per case, the command captures:

- a stateless baseline response using `disable_memory=true`
- a memory-augmented response for the same query
- top retrieval results from `POST /memory/search`
- current retrieval context from `GET /debug/context`
- scenario-generation output when the case includes strategy instructions
- outcome feedback and follow-up behavior when the case includes an outcome and follow-up query

It then computes:

- retrieval precision@5 against the case's expected memory keys
- retrieval coverage against the labeled gold memories
- cue coverage in baseline vs augmented outputs
- strategy success estimate from the recommended scenario, when present
- actual success score from the outcome analysis, when present
- absolute prediction error between estimated and actual success, when present
- whether outcome feedback changed later behavior, when a follow-up case is defined

## Judge-Based Comparison

When a real OpenAI-compatible chat model is configured, the evaluator also asks a judge model to compare baseline and augmented responses on:

- relevance
- usefulness
- personalization
- communication strategy quality

The judge output is treated as one signal, not ground truth.

## Manual Metrics

Some Stage 15 metrics should remain manual.

The generated report includes prompts for:

- user-rated answer usefulness
- user-rated contextual appropriateness
- reviewer notes on retrieval precision labels

That keeps the benchmark honest: the harness automates what it can measure well, and leaves subjective checks explicit.