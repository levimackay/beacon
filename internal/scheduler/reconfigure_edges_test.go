package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/levimackay/beacon/internal/clock"
	"github.com/levimackay/beacon/internal/collect"
	"github.com/levimackay/beacon/internal/incident"
	"github.com/levimackay/beacon/internal/protocol"
	"github.com/levimackay/beacon/internal/scheduler"
)

// Disabling a running target and deleting it both cancel its goroutine
// (refresh's first loop, keyed on t.Enabled), but only DeleteTarget makes it
// through the second loop that calls Machine.Forget, because a disabled
// target is still present in the store and so still counts as "seen".
// Disabling is a pause: an open incident against a target the operator
// paused, rather than removed, must stay on record instead of silently
// being forgotten. No existing test toggles Enabled on a target that is
// already running; TestRun_DisabledTargetIsNeverCollected only covers one
// that starts disabled.
func TestRun_DisablingARunningTargetPreservesMachineState(t *testing.T) {
	tgt := healthyTarget("t1", protocol.KindHost)
	st := newFakeStore(tgt)
	c := newFakeCollector(func(protocol.Target) protocol.Sample { return protocol.Sample{State: protocol.StateDown} })
	machine := incident.NewMachine(clock.Real())
	deps := scheduler.Deps{
		Store:      st,
		Clock:      clock.Real(),
		Collectors: map[protocol.TargetKind]collect.Collector{protocol.KindHost: c},
		Machine:    machine,
		Thresholds: incident.DefaultThresholds(),
		Sleep:      fastSleep,
	}
	sched := scheduler.New(deps)

	cancel, done := runAndCancel(t, sched)
	waitForCall(t, c.calls, 2*time.Second)
	waitForCall(t, c.calls, 2*time.Second)
	deadline := time.Now().Add(2 * time.Second)
	for machine.Current("t1") != protocol.StateDown && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if machine.Current("t1") != protocol.StateDown {
		t.Fatalf("machine never confirmed the down state")
	}

	disabled := tgt
	disabled.Enabled = false
	if err := st.UpsertTarget(context.Background(), disabled); err != nil {
		t.Fatalf("UpsertTarget: %v", err)
	}

	// Give refresh() several cycles to see the disable and cancel the
	// goroutine. fastSleep makes the target's own 5s interval fire every
	// ~2ms of real time, so the buffered calls channel already holds a
	// backlog from before the disable took effect (or from one iteration
	// already in flight when it did); drain that backlog before checking
	// that no further collection happens, or a stale, pre-disable call
	// would be mistaken for a new one.
	time.Sleep(50 * time.Millisecond)
drain:
	for {
		select {
		case <-c.calls:
		default:
			break drain
		}
	}
	assertNoCall(t, c.calls, 100*time.Millisecond)

	if got := machine.Current("t1"); got != protocol.StateDown {
		t.Fatalf("machine.Current(t1) = %v after disabling, want still down (Forget must only run for a delete, not a disable)", got)
	}

	cancel()
	<-done
}
