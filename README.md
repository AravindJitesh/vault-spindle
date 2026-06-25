# vault-spindle

A durable game economy / wallet service: credit battle payouts, atomically purchase items, and claim one-time rewards — with **exactly-once** semantics under retries, concurrency, and process crash.

Built with **Go 1.22** and **PostgreSQL 16**.

## Quick start

```bash
# Start Postgres + API
docker compose up --build -d

# Wait for healthy, then exercise
chmod +x scripts/exercise.sh
./scripts/exercise.sh
```

API listens on **http://localhost:8080**.

## API

All amounts are integer **coins**. Credit and purchase require an **`Idempotency-Key`** header (client-generated, unique per logical operation).

### Credit (battle payout)

```bash
curl -sS -X POST http://localhost:8080/v1/wallets/alice/credit \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: battle-001" \
  -d '{"amount": 500, "reason": "battle-win"}'
```

**200 OK:** `{"balance":500,"reason":"battle-win"}`

### Purchase (atomic debit + item grant)

```bash
curl -sS -X POST http://localhost:8080/v1/wallets/alice/purchase \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: buy-sword-001" \
  -d '{"itemId": "sword", "price": 200}'
```

**200 OK:** `{"balance":300,"itemId":"sword","inventory":["sword"]}`  
**409 Conflict:** `{"error":"insufficient_funds","message":"..."}` — no partial effect; cached on retry.

### Claim one-time reward

```bash
curl -sS -X POST http://localhost:8080/v1/rewards/welcome-pack/claim \
  -H "Content-Type: application/json" \
  -d '{"playerId": "alice"}'
```

**200 OK:** `{"rewardId":"welcome-pack","playerId":"alice","alreadyClaimed":false}`

### Read wallet

```bash
curl -sS http://localhost:8080/v1/wallets/alice
```

**200 OK:** `{"balance":300,"inventory":["sword"],"claimedRewards":["welcome-pack"]}`

### Errors

**400 Bad Request:** `{"error":"invalid_request","message":"..."}` — malformed JSON, missing keys, negative amounts, missing idempotency header.

See [DESIGN.md](./DESIGN.md) for full contract details and limits.

## Tests

Integration tests need Postgres (the compose `db` service is enough):

```bash
docker compose up -d db
export DATABASE_URL="postgres://vault:vault@localhost:5432/vault?sslmode=disable"
go test ./tests/ -v -count=1
```

Coverage highlights:

- Duplicate idempotency keys (credit, purchase, claim)
- 50 concurrent duplicate credits ? one effect
- 20 concurrent purchases on a single-coin wallet ? exactly one success
- Simulated crash (aborted transaction) ? no partial purchase, retry succeeds once
- Invalid input never corrupts wallet state

## Project layout

```
cmd/server/          HTTP server entrypoint
internal/api/        Handlers & validation
internal/store/      Postgres transactions & idempotency
internal/models/     Types & validation helpers
migrations/          SQL schema
tests/               Integration tests
scripts/exercise.sh  curl walkthrough
DESIGN.md            Architecture & dedup strategy
RESILIENCE.md        Split inventory / audit reasoning
AI_DISCLOSURE.md     Tool usage declaration
```

## Design summary

- **Postgres** for ACID durability and row-level locking (`SELECT FOR UPDATE`).
- **Idempotency-Key** stored in the **same transaction** as balance changes; cached HTTP responses for retries.
- **Reward claims** deduped via `UNIQUE(reward_id, player_id)`.
- **`kill -9` mid-request:** uncommitted work rolls back; retry is safe.

## Local development (without Docker for API)

```bash
docker compose up -d db
export DATABASE_URL="postgres://vault:vault@localhost:5432/vault?sslmode=disable"
go run ./cmd/server
```

## License

MIT (assessment submission).
