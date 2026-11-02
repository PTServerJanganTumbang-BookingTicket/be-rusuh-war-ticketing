package idgen

import (
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
)

func TestNew_IsValidULID(t *testing.T) {
	id := New()
	_, err := ulid.Parse(id)
	assert.NoError(t, err)
	assert.Len(t, id, 26)
}

func TestNew_IsUnique(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		id := New()
		_, dup := seen[id]
		assert.False(t, dup, "duplicate id generated: %s", id)
		seen[id] = struct{}{}
	}
}

func TestNewPrefixed_KeepsPrefixAndValidSuffix(t *testing.T) {
	id := NewPrefixed("RB-")
	assert.True(t, strings.HasPrefix(id, "RB-"))
	_, err := ulid.Parse(strings.TrimPrefix(id, "RB-"))
	assert.NoError(t, err)
}
