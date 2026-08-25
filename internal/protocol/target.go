package protocol

import (
	"errors"
	"fmt"
	"time"
)

// Target is a thing Beacon watches.
type Target struct {
	ID              string     `json:"id"`
	Kind            TargetKind `json:"kind"`
	Name            string     `json:"name"`
	Address         string     `json:"address"`
	IntervalSeconds int        `json:"intervalSeconds"`
	ExpectStatus    int        `json:"expectStatus,omitempty"`
	Enabled         bool       `json:"enabled"`
	// AllowPrivate lets this target resolve to an address on the
	// operator's own networks: LAN, loopback, or a Tailscale address.
	// It is off by default so that adding a website target can never
	// become a way to probe the machine Beacon runs on. It never
	// permits the cloud metadata address.
	AllowPrivate bool `json:"allowPrivate,omitempty"`
}

// Validate rejects targets that would be unsafe or nonsensical to schedule.
// It performs no network activity; address reachability is the collector's
// concern and private-range rejection is the SSRF guard's.
func (t Target) Validate() error {
	if t.ID == "" {
		return errors.New("target id is required")
	}
	if t.Name == "" {
		return errors.New("target name is required")
	}
	switch t.Kind {
	case KindHost, KindWebsite, KindService:
	default:
		return fmt.Errorf("unknown target kind %q", t.Kind)
	}
	if t.Kind != KindHost && t.Address == "" {
		return errors.New("target address is required")
	}
	if t.IntervalSeconds < 5 {
		return errors.New("interval must be at least 5 seconds")
	}
	if t.IntervalSeconds > 86400 {
		return errors.New("interval must be at most 86400 seconds")
	}
	if t.ExpectStatus != 0 && (t.ExpectStatus < 100 || t.ExpectStatus > 599) {
		return fmt.Errorf("expected status %d is not a valid HTTP status", t.ExpectStatus)
	}
	return nil
}

// Interval is the target's check interval as a duration.
func (t Target) Interval() time.Duration {
	return time.Duration(t.IntervalSeconds) * time.Second
}

// TargetStatus is a target joined with its most recent observation.
type TargetStatus struct {
	Target     Target             `json:"target"`
	State      State              `json:"state"`
	LatencyMS  float64            `json:"latencyMs"`
	Metrics    map[string]float64 `json:"metrics,omitempty"`
	LastCheck  time.Time          `json:"lastCheck"`
	Error      string             `json:"error,omitempty"`
	CertExpiry *time.Time         `json:"certExpiry,omitempty"`
}
