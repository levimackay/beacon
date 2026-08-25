package incident

import (
	"strings"
	"testing"
	"time"

	"github.com/levimackay/beacon/internal/clock"
	"github.com/levimackay/beacon/internal/protocol"
)

// A collector reporting an outage must carry its explanation through to the
// incident, otherwise Beacon opens incidents that say nothing.
func TestDownSampleKeepsCollectorError(t *testing.T) {
	th := DefaultThresholds()
	state, summary := th.Evaluate(protocol.Sample{
		TargetID: "web-1",
		State:    protocol.StateDown,
		Error:    "connection timeout",
	})
	if state != protocol.StateDown {
		t.Fatalf("state = %q, want down", state)
	}
	if summary != "connection timeout" {
		t.Fatalf("summary = %q, want the collector's error", summary)
	}
}

func TestUnknownSampleKeepsCollectorError(t *testing.T) {
	state, summary := DefaultThresholds().Evaluate(protocol.Sample{
		State: protocol.StateUnknown,
		Error: "sensor read failed",
	})
	if state != protocol.StateUnknown || summary != "sensor read failed" {
		t.Fatalf("Evaluate = (%q, %q)", state, summary)
	}
}

// A certificate expiry arrives from the collector already marked warning.
// The metric verdict is the same severity, so a naive "worse than" test
// would discard the explanation and leave the user with a bare warning.
func TestCertWarningKeepsItsSummary(t *testing.T) {
	state, summary := DefaultThresholds().Evaluate(protocol.Sample{
		TargetID: "web-1",
		State:    protocol.StateWarning,
		Metrics:  map[string]float64{protocol.MetricCertDaysLeft: 5},
	})
	if state != protocol.StateWarning {
		t.Fatalf("state = %q, want warning", state)
	}
	if !strings.Contains(summary, "cert_days_left") || !strings.Contains(summary, "5") {
		t.Fatalf("summary = %q, want it to name the cert metric and value", summary)
	}
}

// A collector-reported warning with no metric breach still needs a reason.
func TestCollectorWarningWithoutMetricBreachUsesItsError(t *testing.T) {
	state, summary := DefaultThresholds().Evaluate(protocol.Sample{
		State:   protocol.StateWarning,
		Error:   "slow response",
		Metrics: map[string]float64{protocol.MetricCPUPercent: 10},
	})
	if state != protocol.StateWarning || summary != "slow response" {
		t.Fatalf("Evaluate = (%q, %q)", state, summary)
	}
}

func TestHealthyStaysSilent(t *testing.T) {
	state, summary := DefaultThresholds().Evaluate(protocol.Sample{
		State:   protocol.StateHealthy,
		Metrics: map[string]float64{protocol.MetricCPUPercent: 12, protocol.MetricDiskPercent: 40},
	})
	if state != protocol.StateHealthy || summary != "" {
		t.Fatalf("Evaluate = (%q, %q), want healthy with no summary", state, summary)
	}
}

// When an incident is confirmed, the summary should describe the reading at
// confirmation time, not the one that happened to start the run.
func TestTransitionCarriesTheLatestSummary(t *testing.T) {
	c := clock.Fake(time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC))
	m := NewMachine(c)

	if tr := m.Observe("host-1", protocol.StateDown, "disk 95.1% (threshold 95%)", c.Now()); tr != nil {
		t.Fatal("single observation should not confirm")
	}
	c.Advance(15 * time.Second)
	tr := m.Observe("host-1", protocol.StateDown, "disk 96.4% (threshold 95%)", c.Now())
	if tr == nil {
		t.Fatal("second consecutive observation should confirm")
	}
	if tr.Summary != "disk 96.4% (threshold 95%)" {
		t.Fatalf("Summary = %q, want the reading at confirmation time", tr.Summary)
	}
	if tr.From != protocol.StateUnknown || tr.To != protocol.StateDown {
		t.Fatalf("transition = %q -> %q", tr.From, tr.To)
	}
}
