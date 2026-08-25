package protocol

import "time"

// Metric keys emitted by collectors. They are stable wire identifiers; the
// store and the threshold evaluator both key off these exact strings.
const (
	MetricCPUPercent    = "cpu_percent"
	MetricMemPercent    = "mem_percent"
	MetricDiskPercent   = "disk_percent"
	MetricLoad1         = "load1"
	MetricUptimeSeconds = "uptime_seconds"
	MetricTempC         = "temp_c"
	MetricCertDaysLeft  = "cert_days_left"
)

// Sample is one observation of one target at one instant.
type Sample struct {
	TargetID   string             `json:"targetId"`
	At         time.Time          `json:"at"`
	State      State              `json:"state"`
	LatencyMS  float64            `json:"latencyMs"`
	Metrics    map[string]float64 `json:"metrics,omitempty"`
	Error      string             `json:"error,omitempty"`
	CertExpiry *time.Time         `json:"certExpiry,omitempty"`
}
