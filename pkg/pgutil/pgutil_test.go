package pgutil

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestNewPool_InvalidDSN covers the ParseConfig error path without a live
// database. An unrecognised URL scheme causes pgxpool.ParseConfig to return an
// error before any network connection is attempted.
func TestNewPool_InvalidDSN(t *testing.T) {
	_, err := NewPool(context.Background(), "badscheme://user:pass@localhost/db", 5, time.Minute)
	assert.Error(t, err)
}
