package redisutil

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClient_OK(t *testing.T) {
	mr := miniredis.RunT(t)
	client, err := NewClient(context.Background(), mr.Addr(), "", 0)
	require.NoError(t, err)
	assert.NotNil(t, client)
	_ = client.Close()
}

func TestNewClient_Unreachable(t *testing.T) {
	_, err := NewClient(context.Background(), "localhost:1", "", 0)
	assert.Error(t, err)
}
