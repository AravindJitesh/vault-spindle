#!/usr/bin/env bash
# Exercise the vault-spindle API against a running instance (default http://localhost:8080).
set -euo pipefail

BASE="${BASE_URL:-http://localhost:8080}"
PLAYER="${PLAYER_ID:-demo-player-$(date +%s)}"

echo "== health =="
curl -sS "$BASE/health" | jq .

echo "== credit 500 coins (battle payout) =="
curl -sS -X POST "$BASE/v1/wallets/$PLAYER/credit" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: credit-$(uuidgen 2>/dev/null || echo 1)" \
  -d '{"amount": 500, "reason": "battle-win"}' | jq .

echo "== purchase sword for 200 =="
KEY="purchase-$(uuidgen 2>/dev/null || echo 2)"
curl -sS -X POST "$BASE/v1/wallets/$PLAYER/purchase" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $KEY" \
  -d '{"itemId": "sword", "price": 200}' | jq .

echo "== duplicate purchase (same Idempotency-Key) =="
curl -sS -X POST "$BASE/v1/wallets/$PLAYER/purchase" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $KEY" \
  -d '{"itemId": "sword", "price": 200}' | jq .

echo "== claim one-time reward =="
curl -sS -X POST "$BASE/v1/rewards/welcome-pack/claim" \
  -H "Content-Type: application/json" \
  -d "{\"playerId\": \"$PLAYER\"}" | jq .

echo "== claim again (idempotent) =="
curl -sS -X POST "$BASE/v1/rewards/welcome-pack/claim" \
  -H "Content-Type: application/json" \
  -d "{\"playerId\": \"$PLAYER\"}" | jq .

echo "== wallet state =="
curl -sS "$BASE/v1/wallets/$PLAYER" | jq .
