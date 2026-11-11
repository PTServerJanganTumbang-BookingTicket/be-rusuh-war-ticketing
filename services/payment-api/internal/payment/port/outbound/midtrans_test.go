package outbound

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Declined is what separates "the gateway said no" from "the gateway said
// nothing". The first is a 200 with status FAILED and a healthy breaker; the
// second is a 503 and a breaker failure, because the instrument may exist.
func TestChargeResult_Declined(t *testing.T) {
	for _, s := range []string{"deny", "cancel", "failure"} {
		assert.True(t, ChargeResult{TransactionStatus: s}.Declined(), s)
	}
	for _, s := range []string{"pending", "settlement", "capture", "expire", "", "DENY"} {
		assert.False(t, ChargeResult{TransactionStatus: s}.Declined(), s)
	}
}
