package protocol

// State is the health of a monitored target.
type State string

const (
	StateHealthy State = "healthy"
	StateWarning State = "warning"
	StateDown    State = "down"
	// StateUnknown means Beacon could not determine the target's health.
	// It is deliberately distinct from StateDown: "I cannot see it" and
	// "it is broken" are different facts and are never conflated.
	StateUnknown State = "unknown"
)

// rank orders states by severity. Unknown sits above warning because an
// unobservable target is a worse operational position than a degraded one,
// but below down because it is not yet a confirmed failure.
func (s State) rank() int {
	switch s {
	case StateDown:
		return 3
	case StateUnknown:
		return 2
	case StateWarning:
		return 1
	case StateHealthy:
		return 0
	}
	return 2
}

// Worse reports whether s is more severe than other.
func (s State) Worse(other State) bool { return s.rank() > other.rank() }

// TargetKind is the category of thing being monitored.
type TargetKind string

const (
	KindHost    TargetKind = "host"
	KindWebsite TargetKind = "website"
	KindService TargetKind = "service"
)
