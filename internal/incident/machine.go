package incident

import (
	"sync"
	"time"

	"github.com/levimackay/beacon/internal/clock"
	"github.com/levimackay/beacon/internal/protocol"
)

// Transition is a confirmed change in a target's state.
type Transition struct {
	TargetID string
	From, To protocol.State
	At       time.Time
	Summary  string
}

// trackedState is a target's confirmed state plus whatever run of a
// different state is currently in progress toward confirmation.
type trackedState struct {
	confirmed      protocol.State
	pending        protocol.State
	pendingSummary string
	streak         int
}

// Machine suppresses flapping: a state change is emitted as a Transition
// only once it has been observed Confirmations times consecutively. This is
// the mechanism that keeps a target bouncing between two states from
// producing a transition (and therefore an alert) on every sample.
type Machine struct {
	Confirmations int

	clock clock.Clock
	mu    sync.Mutex
	state map[string]*trackedState
}

// NewMachine returns a Machine requiring 2 consecutive confirmations.
func NewMachine(c clock.Clock) *Machine {
	return &Machine{
		Confirmations: 2,
		clock:         c,
		state:         make(map[string]*trackedState),
	}
}

// Observe records one observation of s for targetID and returns a non-nil
// Transition only when s differs from the currently confirmed state and has
// now been observed Confirmations times consecutively. A run broken by a
// different state resets the counter. Repeated observations of the already-
// confirmed state emit nothing. Safe for concurrent use across targets and
// within one target.
func (m *Machine) Observe(targetID string, s protocol.State, summary string, at time.Time) *Transition {
	m.mu.Lock()
	defer m.mu.Unlock()

	ts, ok := m.state[targetID]
	if !ok {
		ts = &trackedState{confirmed: protocol.StateUnknown}
		m.state[targetID] = ts
	}

	if s == ts.confirmed {
		ts.pending, ts.streak = "", 0
		return nil
	}

	if s == ts.pending {
		ts.streak++
		// Keep the most recent summary rather than the one that opened
		// the run: at the moment an incident is confirmed, the latest
		// reading is the truthful description of it.
		ts.pendingSummary = summary
	} else {
		ts.pending, ts.pendingSummary, ts.streak = s, summary, 1
	}

	confirmations := m.Confirmations
	if confirmations <= 0 {
		confirmations = 1
	}
	if ts.streak < confirmations {
		return nil
	}

	from := ts.confirmed
	ts.confirmed = s
	ts.pending, ts.streak = "", 0

	return &Transition{TargetID: targetID, From: from, To: s, At: at, Summary: ts.pendingSummary}
}

// Current reports the confirmed state for targetID, or StateUnknown if it
// has never been observed.
func (m *Machine) Current(targetID string) protocol.State {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ts, ok := m.state[targetID]; ok {
		return ts.confirmed
	}
	return protocol.StateUnknown
}

// Forget drops all tracked state for targetID, resetting it to unknown. It
// is called when a target is deleted.
func (m *Machine) Forget(targetID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.state, targetID)
}
