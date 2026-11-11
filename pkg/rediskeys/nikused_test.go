package rediskeys

import "testing"

func TestNIKUsedSharesTheEventHashTagWithTheStockKeys(t *testing.T) {
	ev, err := NewEvent("evt_a")
	if err != nil {
		t.Fatal(err)
	}
	if got := ev.NIKUsed("abc123"); got != "nik:used:{evt_a}:abc123" {
		t.Fatalf("NIKUsed = %q", got)
	}
	// One Lua call touches stock and NIK keys together: they must land in the same cluster slot.
	if tag(ev.NIKUsed("h")) != tag(ev.Stock("cat")) {
		t.Fatalf("hash tags differ: %q vs %q", tag(ev.NIKUsed("h")), tag(ev.Stock("cat")))
	}
}

func tag(key string) string {
	start, end := -1, -1
	for i, c := range key {
		if c == '{' && start < 0 {
			start = i
		}
		if c == '}' && start >= 0 {
			end = i
			break
		}
	}
	return key[start : end+1]
}
