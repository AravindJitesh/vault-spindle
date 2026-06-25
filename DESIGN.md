# vault-spindle — Design

## Overview

`vault-spindle` is a small HTTP wallet/economy service for a game backend. It credits battle payouts, atomically debits currency while granting shop items, and enforces one-time reward claims. **Correctness under retries, concurrency, and process crash** is the primary design constraint.

```
Client ??HTTP??? Go API (stdlib net/http)
                      ?
                      ?
                 PostgreSQL 16
                 (ACID transactions)
```

## Datastore: PostgreSQL

**Why Postgres (and not Redis/in-memory/SQLite alone):**

| Requirement | How Postgres satisfies it |
|---|---|
| Durability across `kill -9` | WAL fsync; committed rows survive process death |
| Atomic debit + item grant | Single `BEGIN…COMMIT` transaction |
| Concurrent wallet updates | `SELECT … FOR UPDATE` row lock serializes writers |
| Claim-once | `PRIMARY KEY (reward_id, player_id)` |
| Idempotent retries | `idempotency_records` table with stored HTTP responses |

Redis alone cannot atomically tie balance mutation to inventory insert without a custom Lua script and still lacks durable multi-key guarantees if configured for speed over fsync. An embedded SQLite file works for single-node durability but makes concurrent wallet locking less battle-tested at scale. Postgres is what I would run in production for ledgers.

**Isolation:** Read Committed (Postgres default). Wallet rows are locked with explicit `FOR UPDATE`, which is stronger than relying on MVCC alone for balance checks.

## Schema

- **`wallets`** — `player_id` PK, `balance BIGINT CHECK (balance >= 0)`
- **`inventory`** — one row per granted item (ordered by `granted_at`)
- **`reward_claims`** — composite PK `(reward_id, player_id)` for claim-once
- **`idempotency_records`** — dedupe key ? serialized HTTP response
- **`ledger_entries`** — append-only audit log (credits, debits, claims)

Migrations run from `migrations/001_init.sql` on startup.

## Exactly-once / deduplication strategy

### Idempotency-Key header (credit & purchase)

All mutating wallet endpoints require `Idempotency-Key: <client-generated string>` (max 256 chars). Flow inside a **single database transaction**:

1. `SELECT … FROM idempotency_records WHERE key = $1 FOR UPDATE`
   - If row exists with `http_status != 0` ? return cached JSON body and status (byte-identical retry).
2. `INSERT INTO idempotency_records (key, http_status=0)` — marks in-flight inside this txn.
3. Perform business logic (credit / purchase with wallet row locked).
4. `UPDATE idempotency_records SET http_status, response_body`.
5. `COMMIT`.

If the process dies at any point before `COMMIT`, Postgres rolls back **everything** including the in-flight idempotency row. A retry starts fresh — no stuck “processing” state.

Concurrent duplicate requests with the same key: the second `INSERT` hits `23505 unique violation`, opens a new txn, reads the completed record, returns cached response.

### Reward claim

Natural idempotency via `UNIQUE(reward_id, player_id)`:

- First claim inserts row ? `alreadyClaimed: false`
- Subsequent claims hit `ON CONFLICT DO NOTHING` ? `alreadyClaimed: true`
- Optional `Idempotency-Key` caches the **first** response for transport retries (same as credit/purchase)

### Key retention

Background goroutine purges `idempotency_records` older than **7 days**. After purge, a retry with the same key would execute again — clients must use fresh keys for new operations. Documented limit; production would align retention with client retry windows (typically 24–72h).

## Atomicity & crash behavior

### Credit

One transaction: reserve idempotency ? `UPDATE wallets SET balance = balance + amount` ? ledger insert ? finalize idempotency ? commit.

### Purchase (the critical path)

One transaction:

1. Reserve idempotency key
2. `SELECT balance FROM wallets WHERE player_id = $1 FOR UPDATE`
3. If `balance < price` ? write 409 response to idempotency table, commit (cached rejection on retry)
4. `UPDATE wallets SET balance = balance - price`
5. `INSERT INTO inventory …`
6. Ledger entry
7. Finalize idempotency ? commit

**Mid-purchase `kill -9`:** If killed before commit, wallet balance and inventory are unchanged. Retry with same idempotency key succeeds once. Never “debited but no item” or “item but no debit”.

### Concurrency

Two purchases racing on a wallet that can afford only one:

- Both acquire wallet row lock sequentially via `FOR UPDATE`
- First commits with lower balance; second sees insufficient funds ? 409
- Exactly one item granted, balance never negative (DB CHECK constraint as backstop)

## API contract

**Currency:** integer **coins** (no fractional units).  
**Limits:** `amount` / `price` ? `[1, 1_000_000_000]`; string fields ? 256 UTF-8 runes; request body ? 1 MiB.

| Endpoint | Success | Error bodies |
|---|---|---|
| `POST …/credit` | **200** `{balance, reason}` | **400** `{error, message}` |
| `POST …/purchase` | **200** `{balance, itemId, inventory}` | **409** `{error:"insufficient_funds", message}` |
| `POST …/rewards/{id}/claim` | **200** `{rewardId, playerId, alreadyClaimed}` | **400** invalid input |
| `GET …/wallets/{id}` | **200** `{balance, inventory, claimedRewards}` | **400** invalid id |

All mutating endpoints require `Content-Type: application/json`. Credit/purchase require `Idempotency-Key`.

**Authoritative pricing:** The server debits the `price` from the request body. In production I would look up catalog prices server-side; for this slice the spec sends `price` in the body and the server enforces solvency against the stored balance, rejecting underfunded purchases atomically.

## Input validation

Validated at the HTTP boundary before any store call:

- Missing/malformed JSON, unknown fields, oversize body ? 400
- Non-positive or overflowing amounts ? 400
- Missing idempotency key on credit/purchase ? 400
- Invalid player/reward IDs ? 400

Malformed input never reaches SQL with unchecked values.

## Operational notes

- Health: `GET /health` checks DB connectivity
- Docker Compose brings up Postgres 16 + API
- Graceful shutdown on SIGTERM (10s drain); `kill -9` relies on Postgres durability, not in-process state
