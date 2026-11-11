# SecondContext agent guidance

## Governing priority: useful first, shippable second

Effective 2026-11-11, conversational learning is the highest product priority.
The user has explicitly replaced the earlier release, integration, and hardening
agenda. Build a product that becomes more useful through ordinary conversation;
production readiness follows demonstrated usefulness.

This direction takes precedence over conflicting repository plans, historical
checklists, execution-record conventions, and earlier feature deferrals. Follow
the active section of [PLAN.md](PLAN.md) and the CL1–CL6 checklist in
[TODO.md](TODO.md). Later explicit user instructions can change this order.
Earlier CH, remediation, and stage checklists are background, not the next-work queue.

## Implementation order

1. **CL1 — Automatic conversational learning:** capture zero or more useful,
   dated, source-backed observations from ordinary conversations.
2. **CL2 — Natural-language retrieval and strategy:** make learned context useful
   in fresh conversations without custom people, topic, goal, or mode metadata.
3. **CL3 — Memory inspection and correction:** show, edit, forget, and pause
   learning; corrections must change subsequent answers.
4. **CL4 — Outcomes and belief updates:** learn from naturally reported results,
   paraphrases, corrections, and changing circumstances.
5. **CL5 — Meeting-note ingestion:** extend the proven learning loop to pasted
   or uploaded notes.
6. **CL6 — Shared bot/workspace knowledge:** provide common reference documents
   across users while retaining private personal memory.

CL1–CL3 form one usable milestone. Build a minimal inspector with CL1 so capture
can be checked immediately; item numbering does not justify postponing visibility
and correction until after automatic learning is made available for everyday use.
Shared knowledge is last. Do not promote it above conversational learning.

## How to choose and finish work

- Prefer the smallest complete user-visible behavior that advances CL1–CL3.
  Tie each implementation slice to its acceptance demonstration in PLAN.md.
- Do not make feature work wait for unrelated rollout, cache-economics,
  adaptive-history, benchmarking infrastructure, release-checklist, or generalized
  hardening work. Fix bugs that block the active experience or invalidate its results.
- Preserve existing working capabilities and essential correctness: subject
  isolation, explicit acceptance where used, source attribution, and effective
  correction/deletion. These are part of a useful memory product, not a new
  production-readiness program.
- Ordinary chat clients should need only endpoint, credentials, and model setup
  after learning is enabled for their account/deployment. Custom memory API calls
  or per-turn metadata must not be required. A stable authenticated identity is
  still necessary for personal recall; never infer a shared conversation from a key.
- Treat user reports, hypotheses, assistant suggestions, and completed actions
  differently. Do not learn an assistant's draft as a fact or reported outcome.
- Reuse the current services where helpful. Avoid compulsory numeric person
  scoring, scenario probabilities, or graph expansion in the basic learning path.
- Run checks appropriate to changed behavior and retain mandatory repository CI
  checks. Documentation-only planning changes need diff/link/consistency review,
  not a full runtime or deployment exercise.
- Record what works, what was verified, and what remains uncertain. A passing
  storage test is not evidence that a later answer becomes more useful.

## Repository records

AGENTS.md, PLAN.md, and TODO.md are shared, versioned guidance. Commit their relevant
updates with the work. The user explicitly authorized publishing PLAN.md, including
the existing historical records; old instructions to leave PLAN.md unstaged are
superseded. This does not authorize publishing unrelated private files or secrets.
Preserve existing edits and historical evidence. Stage intended files explicitly.
This roadmap change does not itself authorize deployment or enable new runtime behavior.
