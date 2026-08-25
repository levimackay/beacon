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

func TestMachineRecoveryAfterConfirmedDownNeedsTwoConfirmedHealthys(t *testing.T) {
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
	tr = m.Observe("t1", protocol.StateHealthy, "", fc.Now())
	if tr == nil {
		t.Fatal("second consecutive healthy = nil, want the recovery transition")
	}
	if tr.From != protocol.StateDown || tr.To != protocol.StateHealthy {
		t.Fatalf("transition = %+v, want Down->Healthy", tr)
	}

	// A third and fourth consecutive down after the confirmed recovery each
	// need their own two confirmations; the first of them alone emits
	// nothing, proving there is no duplicate-alert leak from the old run.
	fc.Advance(15 * time.Second)
	if tr := m.Observe("t1", protocol.StateDown, "down again", fc.Now()); tr != nil {
		t.Fatalf("third down = %+v, want nil", tr)
	}
	fc.Advance(15 * time.Second)
	if tr := m.Observe("t1", protocol.StateDown, "down again", fc.Now()); tr == nil {
		t.Fatal("fourth down (second consecutive) = nil, want a transition")
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
