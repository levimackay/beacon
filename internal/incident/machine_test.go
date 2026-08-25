package incident

import (
	"sync"
	"testing"
	"time"

	"github.com/levimackay/beacon/internal/clock"
	"github.com/levimackay/beacon/internal/protocol"
)

func newTestMachine() (*Machine, *clock.FakeClock) {
	fc := clock.Fake(time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC))
	return NewMachine(fc), fc
}

func TestMachineSingleObservationEmitsNothing(t *testing.T) {
	m, fc := newTestMachine()
	if tr := m.Observe("t1", protocol.StateDown, "down", fc.Now()); tr != nil {
		t.Fatalf("single down = %+v, want nil", tr)
	}
	if tr := m.Observe("t1", protocol.StateHealthy, "", fc.Now()); tr != nil {
		t.Fatalf("single healthy = %+v, want nil", tr)
	}
}

func TestMachineTwoConsecutiveConfirmsHealthyFromUnknown(t *testing.T) {
	m, fc := newTestMachine()
	if tr := m.Observe("t1", protocol.StateHealthy, "", fc.Now()); tr != nil {
		t.Fatalf("first observation = %+v, want nil (not yet confirmed)", tr)
	}
	fc.Advance(15 * time.Second)
	tr := m.Observe("t1", protocol.StateHealthy, "", fc.Now())
	if tr == nil {
		t.Fatal("second consecutive healthy = nil, want a transition")
	}
	if tr.From != protocol.StateUnknown || tr.To != protocol.StateHealthy {
		t.Fatalf("transition = %+v, want Unknown->Healthy", tr)
	}
	if got := m.Current("t1"); got != protocol.StateHealthy {
		t.Fatalf("Current = %v, want healthy", got)
	}
}

func TestMachineTwoConsecutiveDownsEmitExactlyOneTransition(t *testing.T) {
	m, fc := newTestMachine()
	if tr := m.Observe("t1", protocol.StateDown, "disk 96% (threshold 95%)", fc.Now()); tr != nil {
		t.Fatalf("first down = %+v, want nil", tr)
	}
	fc.Advance(15 * time.Second)
	tr := m.Observe("t1", protocol.StateDown, "disk 96% (threshold 95%)", fc.Now())
	if tr == nil {
		t.Fatal("second consecutive down = nil, want a transition")
	}
	if tr.From != protocol.StateUnknown || tr.To != protocol.StateDown {
		t.Fatalf("transition = %+v, want Unknown->Down", tr)
	}
	if tr.Summary == "" {
		t.Fatal("transition summary is empty, want the confirming sample's summary")
	}

	fc.Advance(15 * time.Second)
	if tr := m.Observe("t1", protocol.StateDown, "disk 96% (threshold 95%)", fc.Now()); tr != nil {
		t.Fatalf("third down (already confirmed) = %+v, want nil", tr)
	}
}

func TestMachineBrokenRunEmitsNothing(t *testing.T) {
	m, fc := newTestMachine()
	// down, healthy, down: no run ever reaches two consecutive, so nothing
	// is ever confirmed.
	seq := []protocol.State{protocol.StateDown, protocol.StateHealthy, protocol.StateDown}
	for _, s := range seq {
		if tr := m.Observe("t1", s, "", fc.Now()); tr != nil {
			t.Fatalf("Observe(%v) = %+v, want nil (broken run)", s, tr)
		}
		fc.Advance(15 * time.Second)
	}
	if got := m.Current("t1"); got != protocol.StateUnknown {
		t.Fatalf("Current = %v, want unknown (nothing ever confirmed)", got)
	}
}

// Recovery needs CloseConfirmations (3) consecutive healthy samples, one
// more than the 2 that opened the incident: closing is the boundary where a
// still-flaky target is most likely to produce a single lucky good sample,
// and resolving on that sample only to reopen on the next bad one is the
// same flapping this machine exists to suppress, just moved to the other
// edge.
func TestMachineRecoveryAfterConfirmedDownNeedsThreeConfirmedHealthys(t *testing.T) {
	m, fc := newTestMachine()
	m.Observe("t1", protocol.StateDown, "down", fc.Now())
	fc.Advance(15 * time.Second)
	tr := m.Observe("t1", protocol.StateDown, "down", fc.Now())
	if tr == nil || tr.To != protocol.StateDown {
		t.Fatalf("expected down to confirm, got %+v", tr)
	}

	fc.Advance(15 * time.Second)
	if tr := m.Observe("t1", protocol.StateHealthy, "", fc.Now()); tr != nil {
		t.Fatalf("first healthy after confirmed down = %+v, want nil", tr)
	}
	fc.Advance(15 * time.Second)
	if tr := m.Observe("t1", protocol.StateHealthy, "", fc.Now()); tr != nil {
		t.Fatalf("second healthy after confirmed down = %+v, want nil (close needs 3)", tr)
	}
	if got := m.Current("t1"); got != protocol.StateDown {
		t.Fatalf("Current after 2 healthys = %v, want still down", got)
	}

	fc.Advance(15 * time.Second)
	tr = m.Observe("t1", protocol.StateHealthy, "", fc.Now())
	if tr == nil {
		t.Fatal("third consecutive healthy = nil, want the recovery transition")
	}
	if tr.From != protocol.StateDown || tr.To != protocol.StateHealthy {
		t.Fatalf("transition = %+v, want Down->Healthy", tr)
	}

	// A run of downs after the confirmed recovery needs its own
	// OpenConfirmations (2); the first of them alone emits nothing, proving
	// there is no duplicate-alert leak from the old run.
	fc.Advance(15 * time.Second)
	if tr := m.Observe("t1", protocol.StateDown, "down again", fc.Now()); tr != nil {
		t.Fatalf("first down after recovery = %+v, want nil", tr)
	}
	fc.Advance(15 * time.Second)
	if tr := m.Observe("t1", protocol.StateDown, "down again", fc.Now()); tr == nil {
		t.Fatal("second consecutive down after recovery = nil, want a transition")
	}
}

// A target still down when its second healthy sample lands (of the 3
// closing requires) must not close early and must not lose the streak: the
// down state persists, and one more good sample still closes it.
func TestMachineFlapDuringRecoveryResetsCloseStreakWithoutReopening(t *testing.T) {
	m, fc := newTestMachine()
	m.Observe("t1", protocol.StateDown, "down", fc.Now())
	fc.Advance(15 * time.Second)
	m.Observe("t1", protocol.StateDown, "down", fc.Now())

	fc.Advance(15 * time.Second)
	m.Observe("t1", protocol.StateHealthy, "", fc.Now()) // 1 of 3

	// A single bad sample in the middle of the recovery run breaks the
	// healthy streak. It must not itself re-emit a transition (down is
	// already the confirmed state), and it must force the next healthy run
	// back to needing all 3.
	fc.Advance(15 * time.Second)
	if tr := m.Observe("t1", protocol.StateDown, "still flaky", fc.Now()); tr != nil {
		t.Fatalf("interrupting down sample = %+v, want nil (down is already confirmed)", tr)
	}

	fc.Advance(15 * time.Second)
	m.Observe("t1", protocol.StateHealthy, "", fc.Now()) // 1 of 3, restarted
	fc.Advance(15 * time.Second)
	if tr := m.Observe("t1", protocol.StateHealthy, "", fc.Now()); tr != nil { // 2 of 3
		t.Fatalf("2nd healthy of restarted run = %+v, want nil", tr)
	}
	fc.Advance(15 * time.Second)
	tr := m.Observe("t1", protocol.StateHealthy, "", fc.Now()) // 3 of 3
	if tr == nil || tr.To != protocol.StateHealthy {
		t.Fatalf("3rd healthy of restarted run = %+v, want the recovery transition", tr)
	}
}

// Warning is also an open incident (see design.md: entering any non-healthy
// state opens one), so recovering from Warning is a close too and needs the
// same 3 confirmations as recovering from Down.
func TestMachineRecoveryFromWarningNeedsThreeConfirmedHealthys(t *testing.T) {
	m, fc := newTestMachine()
	m.Observe("t1", protocol.StateWarning, "cpu 86%", fc.Now())
	fc.Advance(15 * time.Second)
	m.Observe("t1", protocol.StateWarning, "cpu 86%", fc.Now())
	if got := m.Current("t1"); got != protocol.StateWarning {
		t.Fatalf("precondition: Current = %v, want warning", got)
	}

	for i := 0; i < 2; i++ {
		fc.Advance(15 * time.Second)
		if tr := m.Observe("t1", protocol.StateHealthy, "", fc.Now()); tr != nil {
			t.Fatalf("healthy %d of 3 = %+v, want nil", i+1, tr)
		}
	}
	fc.Advance(15 * time.Second)
	tr := m.Observe("t1", protocol.StateHealthy, "", fc.Now())
	if tr == nil || tr.From != protocol.StateWarning || tr.To != protocol.StateHealthy {
		t.Fatalf("3rd healthy = %+v, want Warning->Healthy", tr)
	}
}

// A brand-new target's first-ever confirmation is not a "close": nothing
// was ever open for it, so it uses OpenConfirmations (2) even though the
// destination state is Healthy. This is what keeps a freshly added, already
// healthy target from sitting at "unknown" for an extra, pointless sample.
func TestMachineFreshTargetGoingHealthyUsesOpenNotCloseThreshold(t *testing.T) {
	m, fc := newTestMachine()
	if tr := m.Observe("t1", protocol.StateHealthy, "", fc.Now()); tr != nil {
		t.Fatalf("first observation = %+v, want nil", tr)
	}
	fc.Advance(15 * time.Second)
	tr := m.Observe("t1", protocol.StateHealthy, "", fc.Now())
	if tr == nil {
		t.Fatal("second consecutive healthy = nil, want confirmation (Open=2, not Close=3)")
	}
	if tr.From != protocol.StateUnknown || tr.To != protocol.StateHealthy {
		t.Fatalf("transition = %+v, want Unknown->Healthy", tr)
	}
}

// The knob is deliberately overridable, since a caller with an actual reason
// (a target whose check interval is unusually long, say) shouldn't have to
// fork the machine to change it.
func TestMachineConfirmationsAreOverridable(t *testing.T) {
	m, fc := newTestMachine()
	m.OpenConfirmations = 1
	m.CloseConfirmations = 1

	tr := m.Observe("t1", protocol.StateDown, "down", fc.Now())
	if tr == nil || tr.To != protocol.StateDown {
		t.Fatalf("single down with OpenConfirmations=1 = %+v, want an immediate transition", tr)
	}

	fc.Advance(15 * time.Second)
	tr = m.Observe("t1", protocol.StateHealthy, "", fc.Now())
	if tr == nil || tr.To != protocol.StateHealthy {
		t.Fatalf("single healthy with CloseConfirmations=1 = %+v, want an immediate transition", tr)
	}
}

func TestMachineRepeatedConfirmedStateEmitsNothing(t *testing.T) {
	m, fc := newTestMachine()
	m.Observe("t1", protocol.StateDown, "down", fc.Now())
	fc.Advance(15 * time.Second)
	m.Observe("t1", protocol.StateDown, "down", fc.Now())

	for i := 0; i < 5; i++ {
		fc.Advance(15 * time.Second)
		if tr := m.Observe("t1", protocol.StateDown, "down", fc.Now()); tr != nil {
			t.Fatalf("repeat %d of confirmed state = %+v, want nil", i, tr)
		}
	}
}

func TestMachineCurrentReportsConfirmedNotPending(t *testing.T) {
	m, fc := newTestMachine()
	if got := m.Current("t1"); got != protocol.StateUnknown {
		t.Fatalf("Current before any observation = %v, want unknown", got)
	}
	m.Observe("t1", protocol.StateWarning, "cpu 86% (threshold 85%)", fc.Now())
	if got := m.Current("t1"); got != protocol.StateUnknown {
		t.Fatalf("Current after one (pending) observation = %v, want still unknown", got)
	}
	fc.Advance(15 * time.Second)
	m.Observe("t1", protocol.StateWarning, "cpu 86% (threshold 85%)", fc.Now())
	if got := m.Current("t1"); got != protocol.StateWarning {
		t.Fatalf("Current after confirmation = %v, want warning", got)
	}
}

func TestMachineForgetResetsToUnknown(t *testing.T) {
	m, fc := newTestMachine()
	m.Observe("t1", protocol.StateDown, "down", fc.Now())
	fc.Advance(15 * time.Second)
	m.Observe("t1", protocol.StateDown, "down", fc.Now())
	if got := m.Current("t1"); got != protocol.StateDown {
		t.Fatalf("precondition: Current = %v, want down", got)
	}

	m.Forget("t1")
	if got := m.Current("t1"); got != protocol.StateUnknown {
		t.Fatalf("Current after Forget = %v, want unknown", got)
	}

	// And it behaves like a brand-new target: a single observation after
	// Forget must not immediately confirm.
	if tr := m.Observe("t1", protocol.StateDown, "down", fc.Now()); tr != nil {
		t.Fatalf("first observation after Forget = %+v, want nil", tr)
	}
}

func TestMachineConcurrentObserveIsRaceFree(t *testing.T) {
	m, fc := newTestMachine()
	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			state := protocol.StateHealthy
			if i%2 == 0 {
				state = protocol.StateDown
			}
			m.Observe("shared-target", state, "concurrent", fc.Now())
			m.Current("shared-target")
		}()
	}
	wg.Wait()
}
