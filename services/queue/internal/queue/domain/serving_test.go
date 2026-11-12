package domain

import (
	"testing"
	"time"
)

// pick is the client's rule (api-contracts §3.4): the first row whose max_position is at least the position.
func pick(tiers []PollTier, position int64) time.Duration {
	for _, t := range tiers {
		if t.MaxPosition == 0 || position <= t.MaxPosition {
			return t.PollAfter
		}
	}
	return DefaultPollAfter
}

func TestPollTiersAreTheContractsTableWithTheUnlimitedRowLast(t *testing.T) {
	want := []PollTier{
		{10, time.Second}, {100, 2 * time.Second}, {1000, 5 * time.Second}, {0, 10 * time.Second},
	}
	got := PollTiers()
	if len(got) != len(want) {
		t.Fatalf("PollTiers() has %d rows, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if DefaultPollAfter != time.Second {
		t.Errorf("DefaultPollAfter = %v, want 1s", DefaultPollAfter)
	}
}

func TestEachPositionBoundaryPicksTheRequirementsInterval(t *testing.T) {
	cases := []struct {
		position int64
		want     time.Duration
	}{
		{1, time.Second}, {10, time.Second},
		{11, 2 * time.Second}, {100, 2 * time.Second},
		{101, 5 * time.Second}, {1000, 5 * time.Second},
		{1001, 10 * time.Second}, {1_000_000, 10 * time.Second},
	}
	for _, c := range cases {
		if got := pick(PollTiers(), c.position); got != c.want {
			t.Errorf("position %d polls after %v, want %v", c.position, got, c.want)
		}
	}
}

func TestChangingTheReturnedTiersDoesNotChangeTheTable(t *testing.T) {
	PollTiers()[0].PollAfter = time.Hour
	if PollTiers()[0].PollAfter != time.Second {
		t.Fatal("PollTiers() returned the shared table, not a copy")
	}
}
