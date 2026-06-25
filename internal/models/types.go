package models

const (
	MaxAmount          int64 = 1_000_000_000 // 1 billion coins per operation
	MaxStringLen             = 256
	MaxInventoryItems        = 10_000
	IdempotencyKeyHeader     = "Idempotency-Key"
)

type WalletView struct {
	Balance        int64    `json:"balance"`
	Inventory      []string `json:"inventory"`
	ClaimedRewards []string `json:"claimedRewards"`
}

type CreditRequest struct {
	Amount int64  `json:"amount"`
	Reason string `json:"reason"`
}

type CreditResponse struct {
	Balance int64  `json:"balance"`
	Reason  string `json:"reason"`
}

type PurchaseRequest struct {
	ItemID string `json:"itemId"`
	Price  int64  `json:"price"`
}

type PurchaseResponse struct {
	Balance   int64  `json:"balance"`
	ItemID    string `json:"itemId"`
	Inventory []string `json:"inventory"`
}

type ClaimRequest struct {
	PlayerID string `json:"playerId"`
}

type ClaimResponse struct {
	RewardID       string `json:"rewardId"`
	PlayerID       string `json:"playerId"`
	AlreadyClaimed bool   `json:"alreadyClaimed"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
