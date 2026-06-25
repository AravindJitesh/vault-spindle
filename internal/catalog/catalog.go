package catalog

import (
	"errors"
	"fmt"
)

// Authoritative shop prices (integer coins). The server owns pricing; the
// request body must match these values exactly.
var prices = map[string]int64{
	"sword":  200,
	"shield": 200,
	"axe":    100,
	"gem":    50,
}

var ErrUnknownItem = errors.New("unknown catalog item")

// AuthoritativePrice returns the server price when clientPrice matches the catalog.
func AuthoritativePrice(itemID string, clientPrice int64) (int64, error) {
	price, ok := prices[itemID]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrUnknownItem, itemID)
	}
	if clientPrice != price {
		return 0, fmt.Errorf("price %d does not match catalog price %d for item %q", clientPrice, price, itemID)
	}
	return price, nil
}
