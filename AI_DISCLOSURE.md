# AI Tool Disclosure

## Tools used

| Tool | Where | Approx. share |
|---|---|---|
| **Cursor (Claude)** | Architecture decisions, Go implementation, SQL schema, tests, Docker, and documentation (`DESIGN.md`, `RESILIENCE.md`, this file) | ~90% of lines |
| **Human (candidate)** | Requirements review, design approval, manual verification plan, submission cover letter | direction & review |

## How AI was used

- Initial scaffold and implementation of the Go HTTP service, Postgres store layer, and idempotency logic were generated with AI assistance in Cursor.
- Integration tests (duplicate, concurrent, crash-rollback simulation) and Docker setup were AI-drafted then reviewed.
- Design and resilience documents were co-authored: AI produced first drafts from my chosen approach (Postgres + transactional idempotency + outbox pattern for RESILIENCE.md); I verified claims against the actual code.

## What I verified manually

- Idempotency record lives in the **same transaction** as wallet mutations (crash-safe).
- `SELECT FOR UPDATE` on wallet rows for purchase serialization.
- Insufficient-fund rejections are cached in idempotency table (retry gets same 409).
- Tests require a running Postgres (`docker compose up -d db`).

## Honesty note

If any statement in `DESIGN.md` or `RESILIENCE.md` diverges from code behavior, **trust the code and tests**. The outbox/inventory split described in `RESILIENCE.md` is architectural (not fully implemented) — the monolithic purchase transaction in code matches the “before split” baseline described there.
