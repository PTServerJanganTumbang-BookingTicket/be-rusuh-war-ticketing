package delivery

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConsumer_Close_NilFields(t *testing.T) {
	c := &Consumer{}
	assert.NotPanics(t, c.Close)
}
