package rediskeys

import (
	"strings"
	"testing"
)

func TestPaymentHandoffKeysAreNamedAsTheCatalogSays(t *testing.T) {
	cases := map[string]string{
		PaymentHandoff("hc_abc"): "paymenthandoff:hc_abc",
		PaymentSession("ps_abc"): "paymentsession:ps_abc",
		HandoffRateLimit("gt_1"): "rl:handoff:gt_1",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("key = %q, want %q", got, want)
		}
	}
}

func TestPaymentHandoffKeysCarryNoHashTagSoTheyNeverJoinAnEventSlot(t *testing.T) {
	for _, k := range []string{PaymentHandoff("hc_a"), PaymentSession("ps_a"), HandoffRateLimit("gt_a")} {
		if strings.ContainsAny(k, "{}") {
			t.Errorf("%q carries a hash tag; per-customer keys must not", k)
		}
	}
}
