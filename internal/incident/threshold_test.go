package incident

import (
	"strings"
	"testing"

	"github.com/levimackay/beacon/internal/protocol"
)

func healthySample(metrics map[string]float64) protocol.Sample {
	return protocol.Sample{TargetID: "t1", State: protocol.StateHealthy, Metrics: metrics}
}

func TestEvaluateCPUHealthyBelowWarn(t *testing.T) {
	th := DefaultThresholds()
	s := healthySample(map[string]float64{protocol.MetricCPUPercent: 84})
	state, summary := th.Evaluate(s)
	if state != protocol.StateHealthy {
		t.Fatalf("state = %v, want healthy", state)
	}
	if summary != "" {
		t.Fatalf("summary = %q, want empty", summary)
	}
}

func TestEvaluateCPUExactlyWarnThresholdIsWarning(t *testing.T) {
	th := DefaultThresholds()
	state, summary := th.Evaluate(healthySample(map[string]float64{protocol.MetricCPUPercent: 85}))
	if state != protocol.StateWarning {
		t.Fatalf("state = %v, want warning", state)
	}
	if !strings.Contains(summary, "cpu") {
		t.Fatalf("summary = %q, want it to name cpu", summary)
	}
}

func TestEvaluateCPUAboveWarnIsWarning(t *testing.T) {
	th := DefaultThresholds()
	state, _ := th.Evaluate(healthySample(map[string]float64{protocol.MetricCPUPercent: 86}))
	if state != protocol.StateWarning {
		t.Fatalf("state = %v, want warning", state)
	}
}

func TestEvaluateCPUAtDownThresholdIsDown(t *testing.T) {
	th := DefaultThresholds()
	state, summary := th.Evaluate(healthySample(map[string]float64{protocol.MetricCPUPercent: 96}))
	if state != protocol.StateDown {
		t.Fatalf("state = %v, want down", state)
	}
	if !strings.Contains(summary, "cpu") || !strings.Contains(summary, "96") {
		t.Fatalf("summary = %q, want it to name cpu and its value", summary)
	}
}

func TestEvaluateWorstMetricWins(t *testing.T) {
	th := DefaultThresholds()
	state, summary := th.Evaluate(healthySample(map[string]float64{
		protocol.MetricCPUPercent:  86,   // warning
		protocol.MetricDiskPercent: 96.2, // down
	}))
	if state != protocol.StateDown {
		t.Fatalf("state = %v, want down (disk should dominate)", state)
	}
	if !strings.Contains(summary, "disk") {
		t.Fatalf("summary = %q, want it to name disk as the worst metric", summary)
	}
	if !strings.Contains(summary, "96.2") {
		t.Fatalf("summary = %q, want it to carry disk's value", summary)
	}
}

func TestEvaluateUnknownSampleStaysUnknownDespiteMetrics(t *testing.T) {
	th := DefaultThresholds()
	s := protocol.Sample{
		TargetID: "t1",
		State:    protocol.StateUnknown,
		Metrics:  map[string]float64{protocol.MetricCPUPercent: 99, protocol.MetricDiskPercent: 99},
	}
	state, summary := th.Evaluate(s)
	if state != protocol.StateUnknown {
		t.Fatalf("state = %v, want unknown to be authoritative over metrics", state)
	}
	if summary != "" {
		t.Fatalf("summary = %q, want empty for an unknown sample", summary)
	}
}

func TestEvaluateDownSampleStaysDown(t *testing.T) {
	th := DefaultThresholds()
	s := protocol.Sample{
		TargetID: "t1",
		State:    protocol.StateDown,
		Metrics:  map[string]float64{protocol.MetricCPUPercent: 1},
	}
	state, _ := th.Evaluate(s)
	if state != protocol.StateDown {
		t.Fatalf("state = %v, want down to be authoritative over metrics", state)
	}
}

func TestEvaluateCertDaysLeftBelowWarnIsWarning(t *testing.T) {
	th := DefaultThresholds()
	state, summary := th.Evaluate(healthySample(map[string]float64{protocol.MetricCertDaysLeft: 5}))
	if state != protocol.StateWarning {
		t.Fatalf("state = %v, want warning", state)
	}
	if !strings.Contains(summary, "cert") {
		t.Fatalf("summary = %q, want it to name the cert metric", summary)
	}
}

func TestEvaluateCertDaysLeftAboveWarnIsHealthy(t *testing.T) {
	th := DefaultThresholds()
	state, summary := th.Evaluate(healthySample(map[string]float64{protocol.MetricCertDaysLeft: 40}))
	if state != protocol.StateHealthy {
		t.Fatalf("state = %v, want healthy", state)
	}
	if summary != "" {
		t.Fatalf("summary = %q, want empty", summary)
	}
}

func TestEvaluateCertNeverGoesDown(t *testing.T) {
	th := DefaultThresholds()
	// Even a cert that is already expired (0 or negative days left) is a
	// warning, never a down: an expiring cert is not an outage.
	state, _ := th.Evaluate(healthySample(map[string]float64{protocol.MetricCertDaysLeft: -5}))
	if state != protocol.StateWarning {
		t.Fatalf("state = %v, want warning even for an expired cert", state)
	}
}

func TestEvaluateAbsentMetricsDoNotFalselyDegrade(t *testing.T) {
	th := DefaultThresholds()
	// No metrics at all: a healthy sample must stay healthy, not be treated
	// as if every threshold's metric were 0 (which would still be healthy
	// anyway) or, worse, missing-as-triggering.
	state, summary := th.Evaluate(healthySample(nil))
	if state != protocol.StateHealthy {
		t.Fatalf("state = %v, want healthy when no metrics are present", state)
	}
	if summary != "" {
		t.Fatalf("summary = %q, want empty", summary)
	}

	// A present but unrelated metric (mem, comfortably healthy) must not
	// cause disk, which is entirely absent, to be evaluated as if it were 0
	// and somehow trigger anything either.
	state, summary = th.Evaluate(healthySample(map[string]float64{protocol.MetricMemPercent: 10}))
	if state != protocol.StateHealthy {
		t.Fatalf("state = %v, want healthy", state)
	}
	if summary != "" {
		t.Fatalf("summary = %q, want empty", summary)
	}
}
