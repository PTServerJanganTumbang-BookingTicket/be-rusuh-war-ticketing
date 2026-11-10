package usecase

import (
	"testing"
	"time"
)

const half = 500 * time.Millisecond

// run feeds a tracker n ticks of per tickets each and returns the last answer.
func run(r *rateTracker, start time.Time, n int, per int64, ns *int64) int64 {
	var rate int64
	for i := 0; i < n; i++ {
		*ns += per
		rate = r.observe(start.Add(time.Duration(i)*half), *ns, true)
	}
	return rate
}

func TestRateSeedUntilAMinuteOfSamples(t *testing.T) {
	r := newRateTracker(1000, 5, half)
	var ns int64
	if got := run(&r, time.Unix(0, 0), 119, 33, &ns); got != 200 { // 59 s of samples
		t.Fatalf("rate before 60 s = %d, want the seed 200", got)
	}
}

func TestRateSteadySpeedAndStuckQueue(t *testing.T) {
	r := newRateTracker(1000, 5, half)
	var ns int64
	if got := run(&r, time.Unix(0, 0), 121, 25, &ns); got != 3000 {
		t.Fatalf("steady 25 per tick = %d per min, want 3000", got)
	}
	stuck := newRateTracker(1000, 5, half)
	ns = 5
	if got := run(&stuck, time.Unix(0, 0), 121, 0, &ns); got != 0 {
		t.Fatalf("stuck queue = %d, want 0", got)
	}
}

func TestRateRoundsToWholeNumbers(t *testing.T) {
	r := newRateTracker(1000, 5, half)
	r.observe(time.Unix(0, 0), 0, true)
	if got := r.observe(time.Unix(100, 0), 1021, true); got != 613 { // 1021 per 100 s = 612.6
		t.Fatalf("rate = %d, want 613", got)
	}
}

func TestRateHoldsWhileFullyBookedAndRestartsFromSeed(t *testing.T) {
	r := newRateTracker(1000, 5, half)
	var ns int64
	start := time.Unix(0, 0)
	run(&r, start, 241, 5, &ns) // 2 minutes OPEN, 600 per minute
	before := len(r.samples)
	for i := 0; i < 360; i++ { // 3 minutes FULL_BOOKED
		if got := r.observe(start.Add(2*time.Minute+time.Duration(i)*half), ns, false); got != 600 {
			t.Fatalf("rate moved to %d while fully booked", got)
		}
	}
	if len(r.samples) != before {
		t.Fatalf("samples grew from %d to %d while fully booked", before, len(r.samples))
	}
	if fresh := newRateTracker(1000, 5, half); fresh.rate != 200 {
		t.Fatalf("a restarted controller answers %d, want the seed 200", fresh.rate)
	}
}

func TestRateSeedNeverDividesByZero(t *testing.T) {
	if r := newRateTracker(100, 0, half); r.rate != 100 {
		t.Fatalf("seed with 0 minutes = %d, want 100 (treated as 1 minute)", r.rate)
	}
}
