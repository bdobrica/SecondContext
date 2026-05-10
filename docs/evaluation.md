# Evaluation

The repository includes a repeatable evaluation harness for comparing stateless and memory-augmented behavior.

The evaluator is designed to answer one practical question: is the system actually better than a stateless baseline on recurring, context-heavy tasks?

## What It Adds

- a dataset-driven evaluation command at `cmd/eval`
- a seeded benchmark dataset in `cmd/eval/dataset.json`
- automatic baseline vs augmented response generation
- retrieval precision and coverage tracking against manually curated expected memory keys
- optional strategy-success and outcome-feedback checks on cases that include outcomes
- a generated JSON and Markdown report
- explicit placeholders for manual ratings instead of pretending those scores can be automated faithfully
- case-specific judge guidance so each benchmark case can emphasize its real success criteria and failure modes

## Run It

Use the embedded dev server mode:

```bash
make eval
```

In embedded mode, the evaluator uses a fresh evaluation Qdrant collection for each run so old local dev collections do not contaminate retrieval metrics.

Or target a running development API:

```bash
SECOND_CONTEXT_BASE_URL=http://localhost:8080 make eval
```

Reports are written under `.artifacts/evaluation/` by default. You can override that with `EVAL_OUTPUT_DIR`.

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

The rubric is tuned to avoid rewarding answers just because they are longer or more specific-sounding. The judge is instructed to:

- penalize unsupported specifics such as invented numbers, dates, certainty, or technical facts
- reward grounded uncertainty when the case does not supply exact evidence
- treat expected cues as hints rather than a reason to reward keyword stuffing
- apply any case-specific focus and failure-mode guidance stored in the dataset

## Manual Metrics

Some evaluation metrics should remain manual.

The generated report includes prompts for:

- user-rated answer usefulness
- user-rated contextual appropriateness
- reviewer notes on retrieval precision labels

That keeps the benchmark honest: the harness automates what it can measure well, and leaves subjective checks explicit.