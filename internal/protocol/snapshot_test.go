package protocol

import (
	"encoding/json"
	"testing"
	"time"
)

func statuses(states ...State) []TargetStatus {
	out := make([]TargetStatus, len(states))
	for i, s := range states {
		out[i] = TargetStatus{State: s, Target: Target{ID: string(rune('a' + i))}}
	}
	return out
}

func TestOverall(t *testing.T) {
	cases := []struct {
		name string
		in   []TargetStatus
		want State
	}{
		{"empty is unknown, never healthy", nil, StateUnknown},
		{"all healthy", statuses(StateHealthy, StateHealthy), StateHealthy},
		{"one down among healthy", statuses(StateHealthy, StateDown, StateHealthy), StateDown},
		{"warning beats healthy", statuses(StateHealthy, StateWarning), StateWarning},
		{"down beats warning", statuses(StateWarning, StateDown), StateDown},
		{"unknown does not mask down", statuses(StateUnknown, StateDown), StateDown},
		{"unknown beats warning", statuses(StateWarning, StateUnknown), StateUnknown},
		{"unknown beats healthy", statuses(StateHealthy, StateUnknown), StateUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Overall(c.in); got != c.want {
				t.Fatalf("Overall = %q, want %q", got, c.want)
			}
		})
	}
}

func TestTally(t *testing.T) {
	got := Tally(statuses(StateHealthy, StateHealthy, StateWarning, StateDown, StateUnknown))
	want := Counts{Critical: 1, Warning: 1, Healthy: 2, Unknown: 1}
	if got != want {
		t.Fatalf("Tally = %+v, want %+v", got, want)
	}
}

func TestStateWorse(t *testing.T) {
	if !StateDown.Worse(StateUnknown) {
		t.Error("down should be worse than unknown")
	}
	if StateHealthy.Worse(StateWarning) {
		t.Error("healthy should not be worse than warning")
	}
	if StateDown.Worse(StateDown) {
		t.Error("a state is not worse than itself")
	}
}

func TestSnapshotJSONRoundTrip(t *testing.T) {
	resolved := time.Date(2026, 8, 24, 17, 46, 13, 0, time.UTC)
	expiry := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	in := Snapshot{
		GeneratedAt: time.Date(2026, 8, 24, 17, 50, 0, 0, time.UTC),
		Overall:     StateDown,
		Hub:         HubInfo{Version: "0.1.0", Host: "mac", OS: "darwin", Kernel: "25.5.0", UptimeSeconds: 42},
		Counts:      Counts{Critical: 1, Healthy: 2},
		Targets: []TargetStatus{{
			Target:     Target{ID: "web-1", Kind: KindWebsite, Name: "Portfolio", Address: "https://levimackay.com", IntervalSeconds: 60, Enabled: true},
			State:      StateDown,
			LatencyMS:  42.5,
			Metrics:    map[string]float64{MetricCertDaysLeft: 68},
			CertExpiry: &expiry,
			Error:      "connection timeout",
		}},
		OpenIncidents: []Incident{{
			ID: 104, TargetID: "web-1", TargetName: "Portfolio", State: StateDown,
			StartedAt:  time.Date(2026, 8, 24, 17, 42, 0, 0, time.UTC),
			ResolvedAt: &resolved, Summary: "HTTP connection timeout",
		}},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Snapshot
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	again, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("remarshal: %v", err)
	}
	if string(b) != string(again) {
		t.Fatalf("round trip changed payload:\n%s\n%s", b, again)
	}
}

func TestIncidentDuration(t *testing.T) {
	start := time.Date(2026, 8, 24, 17, 42, 0, 0, time.UTC)
	resolved := start.Add(4*time.Minute + 13*time.Second)
	closed := Incident{StartedAt: start, ResolvedAt: &resolved}
	if got := closed.Duration(start.Add(time.Hour)); got != 4*time.Minute+13*time.Second {
		t.Fatalf("closed duration = %v, want 4m13s", got)
	}
	if closed.Open() {
		t.Error("resolved incident reports open")
	}
	open := Incident{StartedAt: start}
	if got := open.Duration(start.Add(90 * time.Second)); got != 90*time.Second {
		t.Fatalf("open duration = %v, want 90s", got)
	}
	if !open.Open() {
		t.Error("unresolved incident reports closed")
	}
}
