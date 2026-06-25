-- Wallets: one row per player, balance in integer coins (smallest unit).
CREATE TABLE IF NOT EXISTS wallets (
    player_id   TEXT PRIMARY KEY,
    balance     BIGINT NOT NULL DEFAULT 0 CHECK (balance >= 0),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Inventory: items owned by a player (item_id may repeat if stackable; we store rows per grant).
CREATE TABLE IF NOT EXISTS inventory (
    id          BIGSERIAL PRIMARY KEY,
    player_id   TEXT NOT NULL REFERENCES wallets(player_id),
    item_id     TEXT NOT NULL,
    granted_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_inventory_player ON inventory(player_id);

-- One-time reward claims: natural idempotency via UNIQUE(reward_id, player_id).
CREATE TABLE IF NOT EXISTS reward_claims (
    reward_id   TEXT NOT NULL,
    player_id   TEXT NOT NULL REFERENCES wallets(player_id),
    claimed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (reward_id, player_id)
);

-- Idempotency records for credit/purchase (and optional claim retries).
-- Stores the full HTTP response so duplicates get byte-identical replies.
CREATE TABLE IF NOT EXISTS idempotency_records (
    idempotency_key TEXT PRIMARY KEY,
    operation       TEXT NOT NULL,
    player_id       TEXT NOT NULL,
    http_status     INT NOT NULL DEFAULT 0,
    response_body   JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_idempotency_player ON idempotency_records(player_id, created_at);

-- Append-only ledger for audit (optional but helps RESILIENCE.md discussion).
CREATE TABLE IF NOT EXISTS ledger_entries (
    id          BIGSERIAL PRIMARY KEY,
    player_id   TEXT NOT NULL,
    entry_type  TEXT NOT NULL,
    amount      BIGINT NOT NULL DEFAULT 0,
    metadata    JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_ledger_player ON ledger_entries(player_id, created_at);
