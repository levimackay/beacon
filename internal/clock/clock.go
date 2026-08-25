// Package clock provides an injectable time source so that incident
// durations and retention windows are deterministic under test.
package clock

import (
	"sync"
	"time"
)

// Clock reports the current time.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Real returns a Clock backed by the system time.
func Real() Clock { return realClock{} }

// FakeClock is a Clock that only moves when told to.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// Fake returns a FakeClock fixed at start.
func Fake(start time.Time) *FakeClock { return &FakeClock{now: start} }

// Now returns the fake clock's current time.
func (f *FakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Advance moves the fake clock forward by d.
func (f *FakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}
