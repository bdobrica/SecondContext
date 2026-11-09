# CH1 / OCH1 contract agreement — 2026-11-09

CH1 adds ADR 0007, the public history v1 contract, authored JSON Schema and synthetic
conformance cases. OCH1 accepts the consumer boundary in Oria ADR 0003 and vendors
only the public schema/fixtures. No Go runtime, route allowlist, migration or live
capability changed. Runtime support belongs to CH2/CH3. PLAN remains an uncommitted
local execution record; TODO carries the tracked dependency status.

Verification with the sibling Oria checkout's installed environment:

- Its `scripts/sync_history_contract.py --source /mnt/www/github/SecondContext --check`
  confirmed byte-identical schema and fixtures.
- JSON Schema 2020-12 validation and 32 consumer schema cases passed, including
  negative cases for mixed finalize variants, missing response IDs, extra fields,
  unknown versions/modes, malformed identifiers, limits and invalid receipts.
- Oria unit and affected history/legacy-adapter/knowledge contract tests passed:
  718 tests; lint, formatting and type checks passed. See the companion
  [verification evidence](https://github.com/bdobrica/OriaEngine/blob/main/docs/evidence/conversation-history-contract.md)
  for commands and the sandbox-related initial test stall/retry.
- `git diff --check` passed in both repositories; local contract/ADR links checked.

No new dependency is introduced here. The existing Oria environment supplies JSON
Schema validation; normal consumer CI uses its vendored files, not this checkout.
Go `make verify`, integration/lifecycle tests, SDK interoperability, Decisions,
provider/cache experiments and deployment were not run for this documentation/schema
slice. CH2/CH3 must implement and prove the normative lifecycle before advertising
`conversation-history-1`; schema validity alone establishes no authorization,
ordering, idempotency, provider generation or erasure guarantee.
