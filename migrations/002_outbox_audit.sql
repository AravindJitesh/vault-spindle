-- Purchase outbox: durable fulfillment intent (same txn as debit in monolithic mode).
CREATE TABLE IF NOT EXISTS purchase_outbox (
    purchase_id   TEXT PRIMARY KEY,
    player_id     TEXT NOT NULL,
    item_id       TEXT NOT NULL,
    price         BIGINT NOT NULL CHECK (price > 0),
    status        TEXT NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending', 'fulfilled', 'failed')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    fulfilled_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_purchase_outbox_pending
    ON purchase_outbox (status, created_at)
    WHERE status = 'pending';

-- Detect duplicate credits sharing the same idempotency key in metadata.
CREATE UNIQUE INDEX IF NOT EXISTS idx_ledger_credit_idempotency
    ON ledger_entries ((metadata->>'idempotency_key'))
    WHERE entry_type = 'credit'
      AND metadata->>'idempotency_key' IS NOT NULL
      AND metadata->>'idempotency_key' <> '';

-- JSONB re-encodes keys on read; TEXT preserves byte-identical idempotent replay bodies.
ALTER TABLE idempotency_records
    ALTER COLUMN response_body TYPE TEXT
    USING CASE WHEN response_body IS NULL THEN NULL ELSE response_body::text END;
