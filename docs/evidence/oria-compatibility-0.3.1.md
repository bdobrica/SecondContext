# Oria command-response compatibility — 0.3.1

The 0.3.0 local memory-command response path built its context packet before
setting the authenticated subject. Oria validates every returned scope echo and
rejected these otherwise successful responses. The same omission affected
legacy Responses and managed explicit/generated acceptance, including cached retries.

The command packet now receives the already resolved authenticated subject. This
restores `metadata.context_packet.user_external_id` without relaxing consumer
validation or changing authentication, memory-command authority, learning opt-in,
managed acceptance, history configuration recipes, or database schemas.

## Verification

- The provider regression covers `Pause learning` and `Can you show my memories?`
  through real HTTP handlers in legacy, explicit and generated modes, checking
  subject/session echoes and stable managed retry responses.
- Focused `TestMemoryCommand` integration tests passed with real isolated
  PostgreSQL/Qdrant and synthetic model responses.
- `make verify` passed, including mandatory dependency-backed tests with no skips.
- Oria's original three-case subject-scope regression passed against this fix.
  Its cross-service fixture is also being updated to distinguish CL2 interpretation
  calls from answer generation, preserving duplicate-generation assertions.

This patch adds no migration beyond 0.3.0's existing schema 000009 and does not
enable conversational learning. Deployment from 0.2.3 still requires the owning
migrations, resolving old-recipe pending turns, backups and live consumer checks.
Publication and deployment evidence belongs to the consuming Oria release record.
