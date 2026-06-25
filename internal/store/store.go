package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrNotFound          = errors.New("not found")
)

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *Store) Migrate(ctx context.Context, sql string) error {
	_, err := s.pool.Exec(ctx, sql)
	return err
}

type IdempotencyHit struct {
	HTTPStatus   int
	ResponseBody []byte
}

// ensureWallet creates a wallet row if missing (balance 0).
func (s *Store) ensureWallet(ctx context.Context, tx pgx.Tx, playerID string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO wallets (player_id, balance)
		VALUES ($1, 0)
		ON CONFLICT (player_id) DO NOTHING
	`, playerID)
	return err
}

// checkIdempotency returns cached response if key exists; nil if new.
func (s *Store) checkIdempotency(ctx context.Context, tx pgx.Tx, key string) (*IdempotencyHit, error) {
	var status int
	var body []byte
	err := tx.QueryRow(ctx, `
		SELECT http_status, response_body
		FROM idempotency_records
		WHERE idempotency_key = $1
		FOR UPDATE
	`, key).Scan(&status, &body)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if status == 0 {
		return nil, fmt.Errorf("idempotency record in progress")
	}
	return &IdempotencyHit{HTTPStatus: status, ResponseBody: body}, nil
}

func (s *Store) reserveIdempotency(ctx context.Context, tx pgx.Tx, key, operation, playerID string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO idempotency_records (idempotency_key, operation, player_id, http_status)
		VALUES ($1, $2, $3, 0)
	`, key, operation, playerID)
	return err
}

func (s *Store) completeIdempotency(ctx context.Context, tx pgx.Tx, key string, httpStatus int, response any) error {
	body, err := json.Marshal(response)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE idempotency_records
		SET http_status = $2, response_body = $3
		WHERE idempotency_key = $1
	`, key, httpStatus, body)
	return err
}

type CreditResult struct {
	Balance int64  `json:"balance"`
	Reason  string `json:"reason"`
}

func (s *Store) Credit(ctx context.Context, playerID, idempotencyKey, reason string, amount int64) (*CreditResult, *IdempotencyHit, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)

	if hit, err := s.checkIdempotency(ctx, tx, idempotencyKey); err != nil {
		return nil, nil, err
	} else if hit != nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, nil, err
		}
		return nil, hit, nil
	}

	if err := s.reserveIdempotency(ctx, tx, idempotencyKey, "credit", playerID); err != nil {
		if isUniqueViolation(err) {
			return s.creditAfterConflict(ctx, playerID, idempotencyKey)
		}
		return nil, nil, err
	}

	if err := s.ensureWallet(ctx, tx, playerID); err != nil {
		return nil, nil, err
	}

	var balance int64
	err = tx.QueryRow(ctx, `
		UPDATE wallets SET balance = balance + $2, updated_at = NOW()
		WHERE player_id = $1
		RETURNING balance
	`, playerID, amount).Scan(&balance)
	if err != nil {
		return nil, nil, err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO ledger_entries (player_id, entry_type, amount, metadata)
		VALUES ($1, 'credit', $2, jsonb_build_object('reason', $3::text, 'idempotency_key', $4::text))
	`, playerID, amount, reason, idempotencyKey)
	if err != nil {
		return nil, nil, err
	}

	result := &CreditResult{Balance: balance, Reason: reason}
	if err := s.completeIdempotency(ctx, tx, idempotencyKey, 200, result); err != nil {
		return nil, nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return result, nil, nil
}

func (s *Store) creditAfterConflict(ctx context.Context, playerID, idempotencyKey string) (*CreditResult, *IdempotencyHit, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)

	hit, err := s.checkIdempotency(ctx, tx, idempotencyKey)
	if err != nil {
		return nil, nil, err
	}
	if hit != nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, nil, err
		}
		return nil, hit, nil
	}
	return nil, nil, fmt.Errorf("idempotency conflict without completed record")
}

type PurchaseResult struct {
	Balance   int64    `json:"balance"`
	ItemID    string   `json:"itemId"`
	Inventory []string `json:"inventory"`
}

func (s *Store) Purchase(ctx context.Context, playerID, idempotencyKey, itemID string, price int64) (*PurchaseResult, *IdempotencyHit, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)

	if hit, err := s.checkIdempotency(ctx, tx, idempotencyKey); err != nil {
		return nil, nil, err
	} else if hit != nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, nil, err
		}
		return nil, hit, nil
	}

	if err := s.reserveIdempotency(ctx, tx, idempotencyKey, "purchase", playerID); err != nil {
		if isUniqueViolation(err) {
			return s.purchaseAfterConflict(ctx, playerID, idempotencyKey)
		}
		return nil, nil, err
	}

	if err := s.ensureWallet(ctx, tx, playerID); err != nil {
		return nil, nil, err
	}

	var balance int64
	err = tx.QueryRow(ctx, `
		SELECT balance FROM wallets WHERE player_id = $1 FOR UPDATE
	`, playerID).Scan(&balance)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrInsufficientFunds
	}
	if err != nil {
		return nil, nil, err
	}

	if balance < price {
		errBody := map[string]string{
			"error":   "insufficient_funds",
			"message": fmt.Sprintf("balance %d is less than price %d", balance, price),
		}
		if err := s.completeIdempotency(ctx, tx, idempotencyKey, 409, errBody); err != nil {
			return nil, nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, nil, err
		}
		return nil, &IdempotencyHit{HTTPStatus: 409, ResponseBody: mustJSON(errBody)}, nil
	}

	newBalance := balance - price
	_, err = tx.Exec(ctx, `
		UPDATE wallets SET balance = $2, updated_at = NOW() WHERE player_id = $1
	`, playerID, newBalance)
	if err != nil {
		return nil, nil, err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO inventory (player_id, item_id) VALUES ($1, $2)
	`, playerID, itemID)
	if err != nil {
		return nil, nil, err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO ledger_entries (player_id, entry_type, amount, metadata)
		VALUES ($1, 'purchase', $2, jsonb_build_object('item_id', $3::text, 'idempotency_key', $4::text))
	`, playerID, -price, itemID, idempotencyKey)
	if err != nil {
		return nil, nil, err
	}

	inventory, err := s.inventoryInTx(ctx, tx, playerID)
	if err != nil {
		return nil, nil, err
	}

	result := &PurchaseResult{
		Balance:   newBalance,
		ItemID:    itemID,
		Inventory: inventory,
	}
	if err := s.completeIdempotency(ctx, tx, idempotencyKey, 200, result); err != nil {
		return nil, nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return result, nil, nil
}

func (s *Store) purchaseAfterConflict(ctx context.Context, playerID, idempotencyKey string) (*PurchaseResult, *IdempotencyHit, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)

	hit, err := s.checkIdempotency(ctx, tx, idempotencyKey)
	if err != nil {
		return nil, nil, err
	}
	if hit != nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, nil, err
		}
		return nil, hit, nil
	}
	return nil, nil, fmt.Errorf("idempotency conflict without completed record")
}

type ClaimResult struct {
	RewardID       string `json:"rewardId"`
	PlayerID       string `json:"playerId"`
	AlreadyClaimed bool   `json:"alreadyClaimed"`
}

func (s *Store) ClaimReward(ctx context.Context, rewardID, playerID, idempotencyKey string) (*ClaimResult, *IdempotencyHit, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)

	if idempotencyKey != "" {
		if hit, err := s.checkIdempotency(ctx, tx, idempotencyKey); err != nil {
			return nil, nil, err
		} else if hit != nil {
			if err := tx.Commit(ctx); err != nil {
				return nil, nil, err
			}
			return nil, hit, nil
		}

		if err := s.reserveIdempotency(ctx, tx, idempotencyKey, "claim", playerID); err != nil {
			if isUniqueViolation(err) {
				return s.claimAfterConflict(ctx, rewardID, playerID, idempotencyKey)
			}
			return nil, nil, err
		}
	}

	if err := s.ensureWallet(ctx, tx, playerID); err != nil {
		return nil, nil, err
	}

	alreadyClaimed := false
	tag, err := tx.Exec(ctx, `
		INSERT INTO reward_claims (reward_id, player_id) VALUES ($1, $2)
		ON CONFLICT (reward_id, player_id) DO NOTHING
	`, rewardID, playerID)
	if err != nil {
		return nil, nil, err
	}
	if tag.RowsAffected() == 0 {
		alreadyClaimed = true
	} else {
		_, err = tx.Exec(ctx, `
			INSERT INTO ledger_entries (player_id, entry_type, amount, metadata)
			VALUES ($1, 'reward_claim', 0, jsonb_build_object('reward_id', $2::text))
		`, playerID, rewardID)
		if err != nil {
			return nil, nil, err
		}
	}

	result := &ClaimResult{
		RewardID:       rewardID,
		PlayerID:       playerID,
		AlreadyClaimed: alreadyClaimed,
	}

	if idempotencyKey != "" {
		if err := s.completeIdempotency(ctx, tx, idempotencyKey, 200, result); err != nil {
			return nil, nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return result, nil, nil
}

func (s *Store) claimAfterConflict(ctx context.Context, rewardID, playerID, idempotencyKey string) (*ClaimResult, *IdempotencyHit, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx)

	hit, err := s.checkIdempotency(ctx, tx, idempotencyKey)
	if err != nil {
		return nil, nil, err
	}
	if hit != nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, nil, err
		}
		return nil, hit, nil
	}
	return nil, nil, fmt.Errorf("idempotency conflict without completed record")
}

func (s *Store) GetWallet(ctx context.Context, playerID string) (balance int64, inventory, claimed []string, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT balance FROM wallets WHERE player_id = $1
	`, playerID).Scan(&balance)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, []string{}, []string{}, nil
	}
	if err != nil {
		return 0, nil, nil, err
	}

	inventory, err = s.inventoryForPlayer(ctx, playerID)
	if err != nil {
		return 0, nil, nil, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT reward_id FROM reward_claims WHERE player_id = $1 ORDER BY claimed_at
	`, playerID)
	if err != nil {
		return 0, nil, nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var rid string
		if err := rows.Scan(&rid); err != nil {
			return 0, nil, nil, err
		}
		claimed = append(claimed, rid)
	}
	return balance, inventory, claimed, rows.Err()
}

func (s *Store) inventoryForPlayer(ctx context.Context, playerID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT item_id FROM inventory WHERE player_id = $1 ORDER BY granted_at, id
	`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []string
	for rows.Next() {
		var item string
		if err := rows.Scan(&item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if items == nil {
		items = []string{}
	}
	return items, rows.Err()
}

func (s *Store) inventoryInTx(ctx context.Context, tx pgx.Tx, playerID string) ([]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT item_id FROM inventory WHERE player_id = $1 ORDER BY granted_at, id
	`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []string
	for rows.Next() {
		var item string
		if err := rows.Scan(&item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if items == nil {
		items = []string{}
	}
	return items, rows.Err()
}

// PurgeOldIdempotency removes records older than retention (background job).
func (s *Store) PurgeOldIdempotency(ctx context.Context, retention time.Duration) (int64, error) {
	cutoff := time.Now().Add(-retention)
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM idempotency_records WHERE created_at < $1
	`, cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
