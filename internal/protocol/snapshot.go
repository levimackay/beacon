package protocol

import "time"

// Counts summarises how many targets sit in each state.
type Counts struct {
	Critical int `json:"critical"`
	Warning  int `json:"warning"`
	Healthy  int `json:"healthy"`
	Unknown  int `json:"unknown"`
}

// HubInfo describes the hub process serving a snapshot.
type HubInfo struct {
	Version       string    `json:"version"`
	Host          string    `json:"host"`
	OS            string    `json:"os"`
	Kernel        string    `json:"kernel"`
	StartedAt     time.Time `json:"startedAt"`
	UptimeSeconds int64     `json:"uptimeSeconds"`
}

// Snapshot is everything a client needs to render Beacon, in one payload.
// The Mac app, the widget and the CLI all consume exactly this.
type Snapshot struct {
	GeneratedAt   time.Time      `json:"generatedAt"`
	Overall       State          `json:"overall"`
	Hub           HubInfo        `json:"hub"`
	Counts        Counts         `json:"counts"`
	Targets       []TargetStatus `json:"targets"`
	OpenIncidents []Incident     `json:"openIncidents"`
}

// Overall derives the worst state across the given statuses. An empty set is
// unknown rather than healthy, so a hub with nothing configured never claims
// that everything is fine.
func Overall(statuses []TargetStatus) State {
	if len(statuses) == 0 {
		return StateUnknown
	}
	worst := StateHealthy
	for _, s := range statuses {
		if s.State.Worse(worst) {
			worst = s.State
		}
	}
	return worst
}

// Tally counts the states present in the given statuses.
func Tally(statuses []TargetStatus) Counts {
	var c Counts
	for _, s := range statuses {
		switch s.State {
		case StateDown:
			c.Critical++
		case StateWarning:
			c.Warning++
		case StateHealthy:
			c.Healthy++
		default:
			c.Unknown++
		}
	}
	return c
}
