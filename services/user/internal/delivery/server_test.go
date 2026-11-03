package delivery

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestServer_Close_NilFields(t *testing.T) {
	// Close must not panic when pool and redis are nil (zero-value Server)
	s := &Server{}
	assert.NotPanics(t, s.Close)
}
