// Package idgen produces ULID-based identifiers.
//
// A ULID is time-sortable (first 48 bits are a millisecond timestamp) and
// collision-safe without a shared counter, which suits stateless handlers.
package idgen

import (
	"crypto/rand"
	"time"

	"github.com/oklog/ulid/v2"
)

// New returns a ULID string, e.g. "01ARZ3NDEKTSV4RRFFQ69G5FAV".
func New() string {
	return ulid.MustNew(ulid.Timestamp(time.Now().UTC()), rand.Reader).String()
}

// NewPrefixed returns a ULID string with a human-readable prefix, e.g. "RB-01ARZ3NDEK...".
func NewPrefixed(prefix string) string {
	return prefix + New()
}
