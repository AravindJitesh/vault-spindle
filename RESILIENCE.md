# Resilience — Split Inventory Service & Audit

## Problem: purchase spans two services

Today, debit and item grant live in one Postgres transaction. In production, **inventory moves to a separate service** over HTTP that can timeout, fail, or duplicate requests, and **cannot share a transaction** with the currency store.

Goal: **exactly-once purchase end-to-end** — player pays once, receives exactly one item, never loses coins without an item or gets a free item.

## Approach: transactional outbox + idempotent consumer

### Write path (wallet service)

Within a **local** DB transaction:

1. Lock wallet row (`FOR UPDATE`), verify balance ? price.
2. Debit balance.
3. Insert **`outbox`** row: `{purchase_id, player_id, item_id, price, status: pending}` with `purchase_id` = client idempotency key (UUID).
4. Insert **`idempotency_records`** with final response placeholder or mark purchase as `pending_fulfillment`.
5. Commit.

The debit is durable before any network call. Crash after commit #5 but before inventory call ? outbox row remains `pending`; a background **relay** retries delivery.

### Fulfillment (relay worker)

Polls `outbox WHERE status = 'pending' FOR UPDATE SKIP LOCKED`:

1. `POST /inventory/grant` with header `Idempotency-Key: {purchase_id}`.
2. On **2xx** ? mark outbox `fulfilled`, finalize wallet idempotency response `{balance, itemId, inventory}`.
3. On **timeout/5xx** ? leave `pending`, retry with exponential backoff.
4. On **4xx non-retryable** ? mark `failed`, **compensating credit** (refund) in a new transaction, return error to client on poll.

### Inventory service contract

Must accept `Idempotency-Key` and dedupe grants:

- Same key ? return same `{grantId, itemId}` without inserting twice.
- Natural key alternative: `UNIQUE(player_id, purchase_id)` on grants table.

### Partial-failure windows

| Window | Risk | Mitigation |
|---|---|---|
| After debit commit, before outbox insert | Debited, no fulfillment scheduled | **Single txn** includes both debit and outbox insert |
| After outbox insert, before inventory ACK | Debited, item not yet granted | Relay retries; inventory dedupes |
| Inventory grants, ACK lost | Relay retries | Idempotent grant ? no double item |
| Inventory grants, wallet never marks fulfilled | Player has item, response stuck pending | Reconciliation job compares outbox vs inventory grants |

The dangerous window is **only** if debit and outbox are not in the same transaction — hence they must be co-located.

### Client experience

Synchronous API can:

- **Option A:** Return `202 Accepted {purchaseId}` immediately; client polls `GET /purchases/{id}` until `fulfilled`.
- **Option B:** Block up to N seconds waiting for outbox relay (simpler for games with short timeouts).

Either way, idempotency key on the original `POST /purchase` ensures transport retries don’t double-debit.

## Detecting & correcting a double-credit bug (no downtime)

### Detection

1. **Ledger invariant:** For each player,  
   `wallet.balance == SUM(ledger_entries.amount WHERE player_id = X)`  
   (credits positive, purchases negative). Schedule a continuous **reconciliation job**; alert on drift.

2. **Idempotency audit:** Group `ledger_entries` by `idempotency_key` in metadata; flag keys with >1 credit entry.

3. **Anomaly scan:** Compare credit velocity per player against baseline; the double-grant last week would show duplicate `entry_type=credit` rows with identical `(reason, amount, timestamp window)` or duplicate idempotency keys if the bug bypassed dedup.

### Correction (online)

1. Insert **compensating ledger entries** (`entry_type=correction, amount=-X`) for affected players in a admin transaction — never silently mutate `balance` without ledger rows.
2. Publish correction event to players (support ticket / in-game mail).
3. Fix root cause (e.g. idempotency check moved outside transaction, or key collision).

### What would have caught it sooner

- **Append-only ledger** with idempotency key on every credit (implemented in this repo).
- **Daily reconciliation** job: `SUM(ledger) vs wallets.balance`.
- **Unique index** on `(entry_type, idempotency_key)` for credit entries — second insert fails loudly instead of double-paying.

## Summary

| Concern | Mechanism |
|---|---|
| Cross-service atomicity | Transactional outbox (local ACID) |
| Inventory duplicate | Idempotency-Key on grant API |
| Inventory timeout | Outbox relay with backoff |
| Stuck purchases | Reconciliation: outbox ? inventory |
| Historical double-credit | Ledger sum invariant + compensating entries |

One page, one principle: **never mutate money without a durable, auditable record; never call external services inside the money transaction — record intent, then deliver.**
