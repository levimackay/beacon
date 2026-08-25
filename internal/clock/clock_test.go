package clock

import (
	"testing"
	"time"
)

func TestFakeDoesNotAdvanceOnItsOwn(t *testing.T) {
	start := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	c := Fake(start)
	first := c.Now()
	time.Sleep(5 * time.Millisecond)
	if !c.Now().Equal(first) {
		t.Fatal("fake clock advanced without Advance")
	}
	c.Advance(90 * time.Second)
	if got := c.Now(); !got.Equal(start.Add(90 * time.Second)) {
		t.Fatalf("Now = %v, want %v", got, start.Add(90*time.Second))
	}
}

func TestRealClockAdvances(t *testing.T) {
	c := Real()
	first := c.Now()
	time.Sleep(2 * time.Millisecond)
	if !c.Now().After(first) {
		t.Fatal("real clock did not advance")
	}
}
