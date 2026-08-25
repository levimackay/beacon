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
// only once it has been observed enough times consecutively. This is the
// mechanism that keeps a target bouncing between two states from producing
// a transition, and therefore an incident and eventually an alert, on every
// sample.
//
// Opening and closing use different thresholds, because they guard against
// different failure modes and a single shared number can't be tuned for
// both at once:
//
//   - OpenConfirmations protects against a single bad sample: one dropped
//     packet, one slow response past a timeout, one CPU spike from a
//     background process, none of which are worth logging as an incident
//     or, once alerting exists, waking anyone up for. A low number here
//     still catches a real outage within a few sample intervals, which is
//     the point of monitoring in the first place.
//   - CloseConfirmations protects against declaring victory too early. A
//     target that is still genuinely broken, a server mid-restart, a site
//     failing most requests but serving one lucky 200, will often produce
//     a single good sample in the middle of a real outage. Resolving on
//     that one sample, only to reopen on the next bad one, is the exact
//     flapping this machine exists to suppress, just relocated from the
//     open boundary to the close boundary instead of removed. A user who
//     sees "resolved" then "down again" a minute later trusts the tool
//     less than one who sees a single incident that took a bit longer to
//     clear. The cost of the higher bar is a few extra sample intervals of
//     showing "down" after a real fix, which is cheap compared to the
//     alternative.
//
// The defaults (2 and 3) are set once here rather than exposed as
// command-line flags or config: a knob only earns its existence once
// someone has a concrete reason to turn it, and "flapping was annoying" is
// a reason to fix the default, not to hand the decision to every operator.
// Both fields stay exported so a caller with an actual reason (a target
// whose check interval is unusually long, say) can still override them
// after construction.
type Machine struct {
	OpenConfirmations  int
	CloseConfirmations int

	clock clock.Clock
	mu    sync.Mutex
	state map[string]*trackedState
}

// NewMachine returns a Machine with Beacon's stock thresholds: 2 consecutive
// samples to open or escalate an incident, 3 to close one.
func NewMachine(c clock.Clock) *Machine {
	return &Machine{
		OpenConfirmations:  2,
		CloseConfirmations: 3,
		clock:              c,
		state:              make(map[string]*trackedState),
	}
}

// Observe records one observation of s for targetID and returns a non-nil
// Transition only when s differs from the currently confirmed state and has
// now been observed enough times consecutively to confirm it (see
// requiredConfirmations). A run broken by a different state resets the
// counter. Repeated observations of the already-confirmed state emit
// nothing. Safe for concurrent use across targets and within one target.
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

	if ts.streak < m.requiredConfirmations(ts.confirmed, s) {
		return nil
	}

	from := ts.confirmed
	ts.confirmed = s
	ts.pending, ts.streak = "", 0

	return &Transition{TargetID: targetID, From: from, To: s, At: at, Summary: ts.pendingSummary}
}

// requiredConfirmations returns how many consecutive samples of to are
// needed to confirm a transition away from a confirmed state of from.
//
// CloseConfirmations only applies to the specific case of resolving an
// incident that is actually open: going to Healthy from Down or Warning.
// Everything else, including a brand-new target's first-ever confirmation
// (from is StateUnknown, the zero value nothing has opened an incident for
// yet), uses OpenConfirmations. Without that carve-out, every freshly added
// target would sit at "unknown" for CloseConfirmations samples before ever
// showing healthy, which is pure added latency: there is no prior incident
// at risk of flapping open again, so there is nothing for the higher bar to
// protect.
func (m *Machine) requiredConfirmations(from, to protocol.State) int {
	n := m.OpenConfirmations
	if to == protocol.StateHealthy && (from == protocol.StateDown || from == protocol.StateWarning) {
		n = m.CloseConfirmations
	}
	if n <= 0 {
		n = 1
	}
	return n
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
