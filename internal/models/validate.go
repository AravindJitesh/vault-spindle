package models

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

func ValidatePlayerID(id string) error {
	if id == "" {
		return fmt.Errorf("playerId is required")
	}
	if utf8.RuneCountInString(id) > MaxStringLen {
		return fmt.Errorf("playerId exceeds max length %d", MaxStringLen)
	}
	return nil
}

func ValidateIdempotencyKey(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("Idempotency-Key header is required")
	}
	if utf8.RuneCountInString(key) > MaxStringLen {
		return fmt.Errorf("Idempotency-Key exceeds max length %d", MaxStringLen)
	}
	return nil
}

func ValidateAmount(amount int64, field string) error {
	if amount <= 0 {
		return fmt.Errorf("%s must be a positive integer", field)
	}
	if amount > MaxAmount {
		return fmt.Errorf("%s exceeds maximum %d", field, MaxAmount)
	}
	return nil
}

func ValidateNonEmptyString(value, field string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	if utf8.RuneCountInString(value) > MaxStringLen {
		return fmt.Errorf("%s exceeds max length %d", field, MaxStringLen)
	}
	return nil
}
