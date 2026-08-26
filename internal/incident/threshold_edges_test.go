package incident

import (
	"strings"
	"testing"

	"github.com/levimackay/beacon/internal/protocol"
)

// mem and temp share the exact warn/down mechanics as cpu but had no direct
// coverage: only cpu and disk were exercised by name, so a broken mem or
// temp spec (wrong key, swapped warn/down) would pass every existing test.

func TestEvaluateMemExactlyWarnThresholdIsWarning(t *testing.T) {
	th := DefaultThresholds()
	state, summary := th.Evaluate(healthySample(map[string]float64{protocol.MetricMemPercent: 85}))
	if state != protocol.StateWarning {
		t.Fatalf("state = %v, want warning", state)
	}
	if !strings.Contains(summary, "mem") {
		t.Fatalf("summary = %q, want it to name mem", summary)
	}
}

func TestEvaluateMemExactlyDownThresholdIsDown(t *testing.T) {
	th := DefaultThresholds()
	state, summary := th.Evaluate(healthySample(map[string]float64{protocol.MetricMemPercent: 95}))
	if state != protocol.StateDown {
		t.Fatalf("state = %v, want down", state)
	}
	if !strings.Contains(summary, "mem") {
		t.Fatalf("summary = %q, want it to name mem", summary)
	}
}

func TestEvaluateTempExactlyWarnThresholdIsWarning(t *testing.T) {
	th := DefaultThresholds()
	state, summary := th.Evaluate(healthySample(map[string]float64{protocol.MetricTempC: 80}))
	if state != protocol.StateWarning {
		t.Fatalf("state = %v, want warning", state)
	}
	if !strings.Contains(summary, "temp") {
		t.Fatalf("summary = %q, want it to name temp", summary)
	}
}

func TestEvaluateTempExactlyDownThresholdIsDown(t *testing.T) {
	th := DefaultThresholds()
	state, summary := th.Evaluate(healthySample(map[string]float64{protocol.MetricTempC: 90}))
	if state != protocol.StateDown {
		t.Fatalf("state = %v, want down", state)
	}
	if !strings.Contains(summary, "temp") {
		t.Fatalf("summary = %q, want it to name temp", summary)
	}
}

// The existing down-threshold test uses 96, one above the 95 cutoff. The
// exact boundary (>=) is the actual edge and was never checked.
func TestEvaluateCPUExactlyAtDownThresholdIsDown(t *testing.T) {
	th := DefaultThresholds()
	state, summary := th.Evaluate(healthySample(map[string]float64{protocol.MetricCPUPercent: 95}))
	if state != protocol.StateDown {
		t.Fatalf("state = %v, want down at the exact down threshold", state)
	}
	if !strings.Contains(summary, "95") {
		t.Fatalf("summary = %q, want it to carry the value", summary)
	}
}

// One below the down cutoff must stay a warning, not down: the >= down
// branch must not fire early.
func TestEvaluateCPUOneBelowDownThresholdIsWarningNotDown(t *testing.T) {
	th := DefaultThresholds()
	state, _ := th.Evaluate(healthySample(map[string]float64{protocol.MetricCPUPercent: 94}))
	if state != protocol.StateWarning {
		t.Fatalf("state = %v, want warning (one below the down threshold)", state)
	}
}

// cert_days_left is inverted: it fires on "fewer days than the threshold",
// so its own boundary sample (value == warn) is healthy, the opposite of
// every other metric where the boundary sample fires. Only well-clear values
// (40 healthy, 5 and -5 warning) were tested before; the boundary itself,
// where a future refactor is most likely to flip the comparison, was not.
func TestEvaluateCertDaysLeftExactlyAtWarnThresholdIsHealthy(t *testing.T) {
	th := DefaultThresholds()
	state, summary := th.Evaluate(healthySample(map[string]float64{protocol.MetricCertDaysLeft: float64(th.CertWarnDays)}))
	if state != protocol.StateHealthy {
		t.Fatalf("state = %v, want healthy at exactly the warn threshold (invert metric)", state)
	}
	if summary != "" {
		t.Fatalf("summary = %q, want empty", summary)
	}
}

func TestEvaluateCertDaysLeftOneBelowWarnThresholdIsWarning(t *testing.T) {
	th := DefaultThresholds()
	state, summary := th.Evaluate(healthySample(map[string]float64{protocol.MetricCertDaysLeft: float64(th.CertWarnDays - 1)}))
	if state != protocol.StateWarning {
		t.Fatalf("state = %v, want warning one day below the threshold", state)
	}
	if !strings.Contains(summary, "cert") {
		t.Fatalf("summary = %q, want it to name the cert metric", summary)
	}
}

// Two metrics at the same severity: Worse() is strict (>), so the second
// match does not overwrite the first. Spec order is cpu, mem, disk, temp,
// cert, so cpu's summary should win over mem's when both are merely
// "warning". This is order-dependent behavior worth pinning down since it
// decides what text ships in an alert.
func TestEvaluateSameSeverityTieKeepsFirstSpecInOrder(t *testing.T) {
	th := DefaultThresholds()
	state, summary := th.Evaluate(healthySample(map[string]float64{
		protocol.MetricCPUPercent: 86,
		protocol.MetricMemPercent: 86,
	}))
	if state != protocol.StateWarning {
		t.Fatalf("state = %v, want warning", state)
	}
	if !strings.Contains(summary, "cpu") {
		t.Fatalf("summary = %q, want cpu (first in spec order) to win the tie", summary)
	}
	if strings.Contains(summary, "mem") {
		t.Fatalf("summary = %q, want mem not to overwrite an equal-severity summary", summary)
	}
}

// A collector-reported Warning is not just a floor for Healthy metrics (see
// TestCollectorWarningWithoutMetricBreachUsesItsError in summary_test.go):
// a metric that computes something strictly worse must still win and
// replace the collector's own summary, not just its own state.
func TestEvaluateCollectorWarningOverriddenByWorseMetricBreach(t *testing.T) {
	th := DefaultThresholds()
	state, summary := th.Evaluate(protocol.Sample{
		TargetID: "t1",
		State:    protocol.StateWarning,
		Error:    "slow response",
		Metrics:  map[string]float64{protocol.MetricCPUPercent: 99},
	})
	if state != protocol.StateDown {
		t.Fatalf("state = %v, want down (metric breach outranks the collector's milder state)", state)
	}
	if summary == "slow response" {
		t.Fatalf("summary = %q, want the metric's summary, not the collector's error", summary)
	}
	if !strings.Contains(summary, "cpu") {
		t.Fatalf("summary = %q, want it to name cpu", summary)
	}
}
