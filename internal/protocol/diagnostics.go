package protocol

import "time"

// StoreStats describes what the hub's database currently holds.
type StoreStats struct {
	Targets       int64 `json:"targets"`
	RawSamples    int64 `json:"rawSamples"`
	Bucket5m      int64 `json:"bucket5m"`
	Bucket1h      int64 `json:"bucket1h"`
	OpenIncidents int64 `json:"openIncidents"`
	SizeBytes     int64 `json:"sizeBytes"`
}

// Diagnostics is the troubleshooting view: what is running, what it can see,
// and when it last did any work.
type Diagnostics struct {
	Hub              HubInfo    `json:"hub"`
	Store            StoreStats `json:"store"`
	LastTick         time.Time  `json:"lastTick"`
	SchedulerHealthy bool       `json:"schedulerHealthy"`
	TailscaleState   string     `json:"tailscaleState"`
	// APILatencyMS is measured by the client, not the server, so it is
	// zero in the server's own response.
	APILatencyMS float64 `json:"apiLatencyMs,omitempty"`
}
