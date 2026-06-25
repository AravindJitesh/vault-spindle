package catalog

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthoritativePrice(t *testing.T) {
	price, err := AuthoritativePrice("sword", 200)
	require.NoError(t, err)
	assert.Equal(t, int64(200), price)

	_, err = AuthoritativePrice("sword", 1)
	require.Error(t, err)

	_, err = AuthoritativePrice("unknown-item", 10)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUnknownItem))
}
