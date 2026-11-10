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

// TestNewPool_unreachableDatabase covers the ping failure path: the DSN parses, nothing listens on the port.
func TestNewPool_unreachableDatabase(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := NewPool(ctx, "postgres://u:p@127.0.0.1:1/db?connect_timeout=1", 0, 0)
	assert.ErrorContains(t, err, "postgres:")
}
