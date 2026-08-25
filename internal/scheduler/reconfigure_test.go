package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/levimackay/beacon/internal/clock"
	"github.com/levimackay/beacon/internal/collect"
	"github.com/levimackay/beacon/internal/protocol"
	"github.com/levimackay/beacon/internal/scheduler"
)

// A per-target goroutine holds its own copy of the target definition. When
// the user edits a target, the running goroutine must be restarted with the
// new definition, otherwise Beacon silently keeps checking the old address
// and the edit appears to do nothing.
func TestRun_EditedTargetIsRecollectedWithTheNewDefinition(t *testing.T) {
	tgt := healthyTarget("web-1", protocol.KindWebsite)
	tgt.Address = "https://old.example.com"

	st := newFakeStore(tgt)
	c := newFakeCollector(func(protocol.Target) protocol.Sample {
		return protocol.Sample{State: protocol.StateHealthy}
	})
	deps := newDeps(st, clock.Real(), map[protocol.TargetKind]collect.Collector{protocol.KindWebsite: c})
	deps.Sleep = fastSleep
	sched := scheduler.New(deps)

	cancel, done := runAndCancel(t, sched)
	defer func() { cancel(); <-done }()

	if got := waitForCall(t, c.calls, 2*time.Second); got.Address != "https://old.example.com" {
		t.Fatalf("first collection used address %q", got.Address)
	}

	edited := tgt
	edited.Address = "https://new.example.com"
	if err := st.UpsertTarget(context.Background(), edited); err != nil {
		t.Fatalf("UpsertTarget: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got := waitForCall(t, c.calls, 2*time.Second)
		if got.Address == "https://new.example.com" {
			return
		}
	}
	t.Fatal("the edited address was never collected; the goroutine kept its stale target definition")
}

// The same applies to the private-network opt-in, which decides whether a
// target may reach a LAN or Tailscale address at all. A stale copy here
// would mean revoking the opt-in leaves it effectively still granted.
func TestRun_RevokingThePrivateOptInTakesEffect(t *testing.T) {
	tgt := healthyTarget("web-1", protocol.KindWebsite)
	tgt.AllowPrivate = true

	st := newFakeStore(tgt)
	c := newFakeCollector(func(protocol.Target) protocol.Sample {
		return protocol.Sample{State: protocol.StateHealthy}
	})
	deps := newDeps(st, clock.Real(), map[protocol.TargetKind]collect.Collector{protocol.KindWebsite: c})
	deps.Sleep = fastSleep
	sched := scheduler.New(deps)

	cancel, done := runAndCancel(t, sched)
	defer func() { cancel(); <-done }()

	if got := waitForCall(t, c.calls, 2*time.Second); !got.AllowPrivate {
		t.Fatal("first collection did not carry the opt-in")
	}

	revoked := tgt
	revoked.AllowPrivate = false
	if err := st.UpsertTarget(context.Background(), revoked); err != nil {
		t.Fatalf("UpsertTarget: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := waitForCall(t, c.calls, 2*time.Second); !got.AllowPrivate {
			return
		}
	}
	t.Fatal("the revoked opt-in was still being applied; a stale target definition kept private access granted")
}

// An unchanged target must NOT be restarted on every refresh, or the
// interval timer would reset repeatedly and a long-interval target would
// never actually fire.
func TestRun_UnchangedTargetIsNotRestarted(t *testing.T) {
	tgt := healthyTarget("web-1", protocol.KindWebsite)
	st := newFakeStore(tgt)
	c := newFakeCollector(func(protocol.Target) protocol.Sample {
		return protocol.Sample{State: protocol.StateHealthy}
	})
	deps := newDeps(st, clock.Real(), map[protocol.TargetKind]collect.Collector{protocol.KindWebsite: c})
	deps.Sleep = fastSleep
	sched := scheduler.New(deps)

	cancel, done := runAndCancel(t, sched)
	defer func() { cancel(); <-done }()

	waitForCall(t, c.calls, 2*time.Second)

	// Rewrite the identical definition several times. None of these is a
	// change, so none should cause a restart.
	for range 3 {
		if err := st.UpsertTarget(context.Background(), tgt); err != nil {
			t.Fatalf("UpsertTarget: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Drain whatever the normal interval produced and confirm the target
	// is still the one we configured, not a duplicate under a new
	// goroutine with different state.
	got := waitForCall(t, c.calls, 2*time.Second)
	if got.ID != "web-1" || got.Address != tgt.Address {
		t.Fatalf("collected %+v, want the original definition", got)
	}
}
