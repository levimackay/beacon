package collect

import (
	"context"
	"testing"

	"github.com/levimackay/beacon/internal/clock"
	"github.com/levimackay/beacon/internal/protocol"
)

func TestHost_Collect(t *testing.T) {
	c := NewHost(clock.Real())
	tgt := protocol.Target{ID: "host1", Kind: protocol.KindHost, Name: "this machine", IntervalSeconds: 30, Enabled: true}

	s := c.Collect(context.Background(), tgt)

	if s.TargetID != "host1" {
		t.Fatalf("targetId = %q, want host1", s.TargetID)
	}
	if s.At.IsZero() {
		t.Fatal("At is zero")
	}
	if s.State != protocol.StateHealthy && s.State != protocol.StateUnknown {
		t.Fatalf("state = %v, want healthy or unknown", s.State)
	}
	if s.State == protocol.StateDown {
		t.Fatal("host collector must never return StateDown")
	}

	cpuPct, ok := s.Metrics[protocol.MetricCPUPercent]
	if !ok {
		t.Fatal("missing cpu_percent metric")
	}
	if cpuPct < 0 || cpuPct > 100 {
		t.Fatalf("cpu_percent = %v, want within 0..100", cpuPct)
	}

	memPct, ok := s.Metrics[protocol.MetricMemPercent]
	if !ok {
		t.Fatal("missing mem_percent metric")
	}
	if memPct < 0 || memPct > 100 {
		t.Fatalf("mem_percent = %v, want within 0..100", memPct)
	}

	uptime, ok := s.Metrics[protocol.MetricUptimeSeconds]
	if !ok {
		t.Fatal("missing uptime_seconds metric")
	}
	if uptime <= 0 {
		t.Fatalf("uptime_seconds = %v, want > 0", uptime)
	}
}
