package protocol

import "time"

// Incident is a period during which a target was not healthy.
type Incident struct {
	ID         int64      `json:"id"`
	TargetID   string     `json:"targetId"`
	TargetName string     `json:"targetName"`
	State      State      `json:"state"`
	StartedAt  time.Time  `json:"startedAt"`
	ResolvedAt *time.Time `json:"resolvedAt,omitempty"`
	Summary    string     `json:"summary"`
}

// Duration is how long the incident lasted, measured to now if still open.
func (i Incident) Duration(now time.Time) time.Duration {
	if i.ResolvedAt != nil {
		return i.ResolvedAt.Sub(i.StartedAt)
	}
	return now.Sub(i.StartedAt)
}

// Open reports whether the incident is still ongoing.
func (i Incident) Open() bool { return i.ResolvedAt == nil }
