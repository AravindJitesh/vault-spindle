package models_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/aravind/vault-spindle/internal/models"
)

func TestValidateAmount(t *testing.T) {
	assert.NoError(t, models.ValidateAmount(1, "amount"))
	assert.NoError(t, models.ValidateAmount(models.MaxAmount, "amount"))
	assert.Error(t, models.ValidateAmount(0, "amount"))
	assert.Error(t, models.ValidateAmount(-1, "amount"))
	assert.Error(t, models.ValidateAmount(models.MaxAmount+1, "amount"))
}

func TestValidateIdempotencyKey(t *testing.T) {
	assert.Error(t, models.ValidateIdempotencyKey(""))
	assert.Error(t, models.ValidateIdempotencyKey("   "))
	assert.NoError(t, models.ValidateIdempotencyKey("key-123"))
	assert.Error(t, models.ValidateIdempotencyKey(strings.Repeat("x", models.MaxStringLen+1)))
}
