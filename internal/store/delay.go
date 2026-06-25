package store

import (
	"os"
	"strconv"
	"time"
)

// purchaseTestDelay returns an optional mid-transaction sleep used by scripts/test-kill9.sh.
// Set TEST_PURCHASE_DELAY_MS on the API process only during crash-resilience testing.
func purchaseTestDelay() time.Duration {
	raw := os.Getenv("TEST_PURCHASE_DELAY_MS")
	if raw == "" {
		return 0
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}
