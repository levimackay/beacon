// Package incident turns raw samples into confirmed, flap-suppressed state
// transitions.
package incident

import (
	"fmt"
	"strconv"

	"github.com/levimackay/beacon/internal/protocol"
)

// Thresholds are the metric levels that turn a sample's metrics into a
// warning or down state. Comparisons are >=, except CertWarnDays which
// fires when the certificate has fewer days left than the threshold.
type Thresholds struct {
	CPUWarn, CPUDown     float64
	MemWarn, MemDown     float64
	DiskWarn, DiskDown   float64
	TempWarnC, TempDownC float64
	CertWarnDays         int
}

// DefaultThresholds returns Beacon's stock levels.
func DefaultThresholds() Thresholds {
	return Thresholds{
		CPUWarn: 85, CPUDown: 95,
		MemWarn: 85, MemDown: 95,
		DiskWarn: 85, DiskDown: 95,
		TempWarnC: 80, TempDownC: 90,
		CertWarnDays: 14,
	}
}

// metricSpec describes how one metric key maps onto warn/down states.
// invert metrics (cert_days_left) fire on "below threshold" rather than
// "at or above", and never carry a down level: an expiring cert is not an
// outage.
type metricSpec struct {
	name    string
	key     string
	unit    string
	warn    float64
	down    float64
	hasDown bool
	invert  bool
}

func (t Thresholds) specs() []metricSpec {
	return []metricSpec{
		{name: "cpu", key: protocol.MetricCPUPercent, unit: "%", warn: t.CPUWarn, down: t.CPUDown, hasDown: true},
		{name: "mem", key: protocol.MetricMemPercent, unit: "%", warn: t.MemWarn, down: t.MemDown, hasDown: true},
		{name: "disk", key: protocol.MetricDiskPercent, unit: "%", warn: t.DiskWarn, down: t.DiskDown, hasDown: true},
		{name: "temp", key: protocol.MetricTempC, unit: "C", warn: t.TempWarnC, down: t.TempDownC, hasDown: true},
		{name: "cert_days_left", key: protocol.MetricCertDaysLeft, warn: float64(t.CertWarnDays), invert: true},
	}
}

// Evaluate returns the state implied by the sample's metrics, never better
// than the state the sample already carries. A sample that already reports
// StateDown or StateUnknown is authoritative: a collector saying "I could
// not reach this" outranks threshold arithmetic, so it passes through with
// the collector's own error as the summary. Missing metrics are skipped,
// never treated as zero. The worst metric wins and the summary names it;
// summary is empty when the result is healthy.
func (t Thresholds) Evaluate(s protocol.Sample) (protocol.State, string) {
	if s.State == protocol.StateDown || s.State == protocol.StateUnknown {
		return s.State, s.Error
	}

	// The metric verdict is computed independently of the sample's own
	// state so that a metric breach at the same severity the collector
	// already reported still contributes its summary. Without this, a
	// certificate-expiry warning would arrive with no explanation.
	metricState := protocol.StateHealthy
	summary := ""

	for _, spec := range t.specs() {
		value, ok := s.Metrics[spec.key]
		if !ok {
			continue
		}

		var st protocol.State
		var threshold float64
		switch {
		case spec.invert:
			if value >= spec.warn {
				continue
			}
			st, threshold = protocol.StateWarning, spec.warn
		case spec.hasDown && value >= spec.down:
			st, threshold = protocol.StateDown, spec.down
		case value >= spec.warn:
			st, threshold = protocol.StateWarning, spec.warn
		default:
			continue
		}

		if st.Worse(metricState) {
			metricState = st
			summary = fmt.Sprintf("%s %s%s (threshold %s%s)", spec.name, formatNum(value), spec.unit, formatNum(threshold), spec.unit)
		}
	}

	if s.State.Worse(metricState) {
		return s.State, s.Error
	}
	if metricState == protocol.StateHealthy {
		return protocol.StateHealthy, ""
	}
	return metricState, summary
}

// formatNum renders a float without a forced decimal (95, not 95.000000)
// while preserving whatever precision the value actually carries.
func formatNum(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
