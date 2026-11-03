package delivery

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestServer_Close_NilFields(t *testing.T) {
	s := &Server{}
	assert.NotPanics(t, s.Close)
}
