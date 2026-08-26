package incident

import (
	"time"

	"testing"

	"github.com/levimackay/beacon/internal/protocol"
)

// requiredConfirmations only raises the bar to CloseConfirmations for the
// specific transition of Down or Warning going to Healthy. Escalating an
// already-open incident from Warning to Down is not a close, so it must
// still use the low OpenConfirmations bar. Existing tests only ever
// escalate from Unknown (a brand-new target), never from an already
// confirmed Warning, so this path was untested.
func TestMachineWarningToDownEscalationUsesOpenNotClose(t *testing.T) {
	m, fc := newTestMachine()
	m.Observe("t1", protocol.StateWarning, "cpu 86%", fc.Now())
	fc.Advance(15 * time.Second)
	m.Observe("t1", protocol.StateWarning, "cpu 86%", fc.Now())
	if got := m.Current("t1"); got != protocol.StateWarning {
		t.Fatalf("precondition: Current = %v, want warning", got)
	}

	fc.Advance(15 * time.Second)
	if tr := m.Observe("t1", protocol.StateDown, "disk 96%", fc.Now()); tr != nil {
		t.Fatalf("first down after confirmed warning = %+v, want nil", tr)
	}
	fc.Advance(15 * time.Second)
	tr := m.Observe("t1", protocol.StateDown, "disk 96%", fc.Now())
	if tr == nil {
		t.Fatal("second consecutive down = nil, want escalation confirmed with Open=2, not Close=3")
	}
	if tr.From != protocol.StateWarning || tr.To != protocol.StateDown {
		t.Fatalf("transition = %+v, want Warning->Down", tr)
	}
}

// The mirror case: de-escalating from Down to Warning is not a close either
// (the destination is not Healthy), so it also uses OpenConfirmations, not
// CloseConfirmations. Every existing recovery test goes straight to Healthy;
// none stop at the intermediate Warning state.
func TestMachineDownToWarningDeescalationUsesOpenNotClose(t *testing.T) {
	m, fc := newTestMachine()
	m.Observe("t1", protocol.StateDown, "disk 96%", fc.Now())
	fc.Advance(15 * time.Second)
	m.Observe("t1", protocol.StateDown, "disk 96%", fc.Now())
	if got := m.Current("t1"); got != protocol.StateDown {
		t.Fatalf("precondition: Current = %v, want down", got)
	}

	fc.Advance(15 * time.Second)
	if tr := m.Observe("t1", protocol.StateWarning, "disk 88%", fc.Now()); tr != nil {
		t.Fatalf("first warning after confirmed down = %+v, want nil", tr)
	}
	fc.Advance(15 * time.Second)
	tr := m.Observe("t1", protocol.StateWarning, "disk 88%", fc.Now())
	if tr == nil {
		t.Fatal("second consecutive warning = nil, want de-escalation confirmed with Open=2, not Close=3")
	}
	if tr.From != protocol.StateDown || tr.To != protocol.StateWarning {
		t.Fatalf("transition = %+v, want Down->Warning", tr)
	}
	if got := m.Current("t1"); got != protocol.StateWarning {
		t.Fatalf("Current after de-escalation = %v, want warning (not skipped to healthy)", got)
	}
}

// requiredConfirmations guards n<=0 by treating it as 1. A caller who zeroes
// or negatively configures a threshold (a construction bug, or an operator
// override gone wrong) should get an immediate confirmation on every sample
// rather than a threshold that can never be met.
func TestMachineNonPositiveConfirmationsTreatedAsOne(t *testing.T) {
	t.Run("zero open", func(t *testing.T) {
		m, fc := newTestMachine()
		m.OpenConfirmations = 0
		tr := m.Observe("t1", protocol.StateDown, "down", fc.Now())
		if tr == nil || tr.To != protocol.StateDown {
			t.Fatalf("single down with OpenConfirmations=0 = %+v, want an immediate transition", tr)
		}
	})
	t.Run("negative open", func(t *testing.T) {
		m, fc := newTestMachine()
		m.OpenConfirmations = -3
		tr := m.Observe("t1", protocol.StateDown, "down", fc.Now())
		if tr == nil || tr.To != protocol.StateDown {
			t.Fatalf("single down with OpenConfirmations=-3 = %+v, want an immediate transition", tr)
		}
	})
	t.Run("zero close", func(t *testing.T) {
		m, fc := newTestMachine()
		m.CloseConfirmations = 0
		m.Observe("t1", protocol.StateDown, "down", fc.Now())
		fc.Advance(15 * time.Second)
		m.Observe("t1", protocol.StateDown, "down", fc.Now())

		fc.Advance(15 * time.Second)
		tr := m.Observe("t1", protocol.StateHealthy, "", fc.Now())
		if tr == nil || tr.To != protocol.StateHealthy {
			t.Fatalf("single healthy with CloseConfirmations=0 = %+v, want an immediate transition", tr)
		}
	})
}

// Going unreachable is not a close (the destination is not Healthy), so it
// uses OpenConfirmations regardless of the state it interrupts. No existing
// test drives a confirmed target to Unknown.
func TestMachineHealthyToUnknownUsesOpenConfirmations(t *testing.T) {
	m, fc := newTestMachine()
	m.Observe("t1", protocol.StateHealthy, "", fc.Now())
	fc.Advance(15 * time.Second)
	m.Observe("t1", protocol.StateHealthy, "", fc.Now())
	if got := m.Current("t1"); got != protocol.StateHealthy {
		t.Fatalf("precondition: Current = %v, want healthy", got)
	}

	fc.Advance(15 * time.Second)
	if tr := m.Observe("t1", protocol.StateUnknown, "no route to host", fc.Now()); tr != nil {
		t.Fatalf("first unknown after confirmed healthy = %+v, want nil", tr)
	}
	fc.Advance(15 * time.Second)
	tr := m.Observe("t1", protocol.StateUnknown, "no route to host", fc.Now())
	if tr == nil {
		t.Fatal("second consecutive unknown = nil, want confirmation with Open=2")
	}
	if tr.From != protocol.StateHealthy || tr.To != protocol.StateUnknown {
		t.Fatalf("transition = %+v, want Healthy->Unknown", tr)
	}
}
