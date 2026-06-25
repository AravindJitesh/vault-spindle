# AI Tool Disclosure

## Tools used

| Tool | Where | Approx. share |
|---|---|---|
| **Cursor (Claude)** | Architecture, Go implementation, SQL schema, tests, Docker, scripts, documentation | ~90% of lines |
| **Human (candidate)** | Requirements review, design approval, manual verification, submission email | direction & review |

## How AI was used

- Initial scaffold and core implementation (Go HTTP service, Postgres store, idempotency) were generated with AI assistance in Cursor.
- Gap-closure work (catalog pricing, outbox table, ledger reconciliation, kill-9 script, extra tests, doc updates) was AI-assisted after human review of the assessment rubric.
- `DESIGN.md` and `RESILIENCE.md` were co-authored; claims were checked against code and tests.

## What I verified manually

- Idempotency record lives in the **same transaction** as wallet mutations (crash-safe).
- `SELECT FOR UPDATE` on wallet rows for purchase serialization.
- Insufficient-fund rejections are cached in idempotency table (retry gets same 409).
- `make test-integration` and `make test-kill9` against Docker + Postgres.
- Catalog rejects client price mismatches; ledger reconciliation matches wallet balance.

## Honesty note

- **`purchase_outbox`** is implemented for monolithic purchases (pending ? fulfilled in one transaction). The **external inventory relay** described in `RESILIENCE.md` is not built — that remains the production split-service design.
- **`scripts/test-kill9.sh`** uses `TEST_PURCHASE_DELAY_MS` to widen the kill window; production code ignores this env var unless set for testing.
- If any doc statement diverges from behavior, **trust the code and tests**.
