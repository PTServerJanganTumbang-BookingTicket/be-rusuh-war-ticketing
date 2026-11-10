package usecase

import (
	"math"
	"time"
)

// rateTracker measures ticket numbers now_serving passed per minute over the last 60 s of OPEN time.
// Until 60 s of OPEN samples exist it answers the seed MAX_CONCURRENT_SLOTS / ETA_SEED_SLOT_MINUTES.
// FULL_BOOKED time adds no sample and the last answer stays.
type rateTracker struct {
	seed     int64
	openSecs float64 // seconds of OPEN time observed
	lastTick time.Time
	lastOpen bool
	samples  []rateSample
	rate     int64
	tickSecs float64
}

type rateSample struct {
	at float64 // openSecs when taken
	ns int64
}

const rateWindow = 60.0

func newRateTracker(maxSlots, seedMinutes int64, tick time.Duration) rateTracker {
	if seedMinutes < 1 {
		seedMinutes = 1
	}
	seed := maxSlots / seedMinutes
	return rateTracker{seed: seed, rate: seed, tickSecs: tick.Seconds()}
}

// RateSamples is the number of samples kept (tests assert none are added while fully booked).

// observe records one tick and returns the whole-number rate to publish.
func (r *rateTracker) observe(now time.Time, nowServing int64, open bool) int64 {
	if !open {
		r.lastOpen = false
		return r.rate
	}
	switch {
	case r.lastOpen:
		r.openSecs += now.Sub(r.lastTick).Seconds()
	case len(r.samples) > 0:
		r.openSecs += r.tickSecs // reopened after FULL_BOOKED: that tick counts as one tick of OPEN time
	}
	r.lastTick, r.lastOpen = now, true
	r.samples = append(r.samples, rateSample{at: r.openSecs, ns: nowServing})
	if r.openSecs < rateWindow-1e-9 {
		return r.rate // still the seed
	}
	// Drop samples older than the window, keeping the newest one that is at least 60 s old.
	cut := r.openSecs - rateWindow
	i := 0
	for i+1 < len(r.samples) && r.samples[i+1].at <= cut+1e-9 {
		i++
	}
	r.samples = r.samples[i:]
	// Ticket numbers passed per minute over the real span of the window (60 s or a little more).
	span := r.openSecs - r.samples[0].at
	if span <= 0 {
		return r.rate
	}
	r.rate = int64(math.Round(float64(nowServing-r.samples[0].ns) * rateWindow / span))
	return r.rate
}
