package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var ErrBalanceDrift = errors.New("wallet balance does not match ledger sum")

// ReconcilePlayer compares wallet.balance to SUM(ledger_entries.amount).
// Returns wallet balance, ledger sum, and ErrBalanceDrift when they differ.
func (s *Store) ReconcilePlayer(ctx context.Context, playerID string) (walletBalance int64, ledgerSum int64, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT COALESCE((SELECT balance FROM wallets WHERE player_id = $1), 0)
	`, playerID).Scan(&walletBalance)
	if err != nil {
		return 0, 0, err
	}

	err = s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0) FROM ledger_entries WHERE player_id = $1
	`, playerID).Scan(&ledgerSum)
	if err != nil {
		return 0, 0, err
	}

	if walletBalance != ledgerSum {
		return walletBalance, ledgerSum, fmt.Errorf("%w: wallet=%d ledger=%d", ErrBalanceDrift, walletBalance, ledgerSum)
	}
	return walletBalance, ledgerSum, nil
}

// OutboxStatus returns the purchase outbox row status for an idempotency key.
func (s *Store) OutboxStatus(ctx context.Context, purchaseID string) (string, error) {
	var status string
	err := s.pool.QueryRow(ctx, `
		SELECT status FROM purchase_outbox WHERE purchase_id = $1
	`, purchaseID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return status, err
}
