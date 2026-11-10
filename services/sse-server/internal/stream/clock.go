// Package stream is the queue status stream: one SSE connection per waiting
// customer, one shared payload per event, no per-customer state.
package stream

import "time"

// Clock is the server's time source. Tests replace it to move time by hand.
type Clock interface {
	Now() time.Time
	NewTicker(d time.Duration) Ticker
}

// Ticker is a time.Ticker behind an interface.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

type realClock struct{}

// RealClock is the wall clock.
func RealClock() Clock { return realClock{} }

func (realClock) Now() time.Time                   { return time.Now() }
func (realClock) NewTicker(d time.Duration) Ticker { return realTicker{time.NewTicker(d)} }

type realTicker struct{ *time.Ticker }

func (t realTicker) C() <-chan time.Time { return t.Ticker.C }
