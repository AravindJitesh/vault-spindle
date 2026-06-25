#!/usr/bin/env bash
# Kill -9 the API mid-purchase and assert exactly-once behavior after restart.
#
# Requires: docker compose stack, curl, jq
# Usage: ./scripts/test-kill9.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

export DOCKER_HOST="${DOCKER_HOST:-unix://${HOME}/.colima/default/docker.sock}"

if docker compose version >/dev/null 2>&1; then
  COMPOSE=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
  COMPOSE=(docker-compose)
else
  echo "docker compose or docker-compose required" >&2
  exit 1
fi

BASE="${BASE_URL:-http://localhost:8080}"
PLAYER="kill9-$(date +%s)"
FUND_KEY="kill9-fund-${PLAYER}"
KEY="kill9-purchase-${PLAYER}"
DELAY_MS="${TEST_PURCHASE_DELAY_MS:-10000}"

wait_healthy() {
  for _ in $(seq 1 30); do
    if curl -sf "$BASE/health" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo "API did not become healthy" >&2
  return 1
}

echo "== starting stack with purchase delay =="
"${COMPOSE[@]}" up -d db
"${COMPOSE[@]}" stop api 2>/dev/null || true
TEST_PURCHASE_DELAY_MS="$DELAY_MS" "${COMPOSE[@]}" up --build -d api
wait_healthy

echo "== seed wallet =="
FUND_RESP="$(curl -sS -X POST "$BASE/v1/wallets/$PLAYER/credit" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $FUND_KEY" \
  -d '{"amount": 100, "reason": "seed"}')"
echo "$FUND_RESP" | jq .
if [[ "$(echo "$FUND_RESP" | jq -r '.balance // empty')" != "100" ]]; then
  echo "FAIL: seed credit did not reach balance 100" >&2
  exit 1
fi

echo "== start purchase (will pause mid-transaction for ${DELAY_MS}ms) =="
curl -sS --max-time 60 -X POST "$BASE/v1/wallets/$PLAYER/purchase" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $KEY" \
  -d '{"itemId": "axe", "price": 100}' >/tmp/kill9-purchase.json &
CURL_PID=$!

sleep 2
echo "== SIGKILL api container =="
"${COMPOSE[@]}" kill -s SIGKILL api || true
wait "$CURL_PID" 2>/dev/null || true

echo "== restart api without delay =="
"${COMPOSE[@]}" stop api 2>/dev/null || true
"${COMPOSE[@]}" up -d api
wait_healthy

echo "== wallet after crash (should be unchanged) =="
curl -sS "$BASE/v1/wallets/$PLAYER" | jq .

BALANCE_AFTER_CRASH="$(curl -sS "$BASE/v1/wallets/$PLAYER" | jq -r '.balance')"
INV_LEN_AFTER_CRASH="$(curl -sS "$BASE/v1/wallets/$PLAYER" | jq -r '.inventory | length')"
if [[ "$BALANCE_AFTER_CRASH" != "100" || "$INV_LEN_AFTER_CRASH" != "0" ]]; then
  echo "FAIL: expected balance=100 and empty inventory after crash, got balance=$BALANCE_AFTER_CRASH inventory_count=$INV_LEN_AFTER_CRASH" >&2
  exit 1
fi

echo "== retry purchase with same idempotency key =="
RESP="$(curl -sS -X POST "$BASE/v1/wallets/$PLAYER/purchase" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $KEY" \
  -d '{"itemId": "axe", "price": 100}')"
echo "$RESP" | jq .

echo "== duplicate retry (cached response) =="
RESP2="$(curl -sS -X POST "$BASE/v1/wallets/$PLAYER/purchase" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $KEY" \
  -d '{"itemId": "axe", "price": 100}')"
echo "$RESP2" | jq .

if [[ "$(echo "$RESP" | jq -cS .)" != "$(echo "$RESP2" | jq -cS .)" ]]; then
  echo "FAIL: duplicate retry response differed semantically" >&2
  exit 1
fi

FINAL="$(curl -sS "$BASE/v1/wallets/$PLAYER")"
echo "== final wallet =="
echo "$FINAL" | jq .

FINAL_BALANCE="$(echo "$FINAL" | jq -r '.balance')"
FINAL_INV="$(echo "$FINAL" | jq -r '.inventory | length')"
if [[ "$FINAL_BALANCE" != "0" || "$FINAL_INV" != "1" ]]; then
  echo "FAIL: expected balance=0 and one item, got balance=$FINAL_BALANCE inventory_count=$FINAL_INV" >&2
  exit 1
fi

echo "PASS: kill -9 mid-purchase left no partial effect; retry succeeded exactly once"
