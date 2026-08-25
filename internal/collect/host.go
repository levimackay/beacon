package collect

import (
	"context"
	"errors"
	"time"

	"github.com/levimackay/beacon/internal/clock"
	"github.com/levimackay/beacon/internal/protocol"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/sensors"
)

// cpuSampleWindow is how long cpu.Percent blocks measuring utilization.
const cpuSampleWindow = 300 * time.Millisecond

// sensibleTempMax discards sensor readings above this as noise (a bogus
// sensor, not an actual 150C+ reading).
const sensibleTempMax = 150.0

type hostCollector struct {
	clock clock.Clock
}

// NewHost returns a Collector that reads local system metrics via gopsutil.
// It never reports StateDown: a metric it cannot read is simply omitted (or,
// if nothing at all could be read, the whole sample is StateUnknown).
// Threshold-based downgrading to warning/down is internal/incident's job,
// not the collector's — a collector only reports what it observed.
func NewHost(c clock.Clock) Collector {
	return &hostCollector{clock: c}
}

func (h *hostCollector) Collect(ctx context.Context, t protocol.Target) protocol.Sample {
	s := protocol.Sample{
		TargetID: t.ID,
		At:       h.clock.Now(),
		Metrics:  make(map[string]float64),
	}

	var errs []error

	if pct, err := cpu.PercentWithContext(ctx, cpuSampleWindow, false); err != nil {
		errs = append(errs, err)
	} else if len(pct) > 0 {
		s.Metrics[protocol.MetricCPUPercent] = pct[0]
	}

	if vm, err := mem.VirtualMemoryWithContext(ctx); err != nil {
		errs = append(errs, err)
	} else {
		s.Metrics[protocol.MetricMemPercent] = vm.UsedPercent
	}

	if du, err := disk.UsageWithContext(ctx, "/"); err != nil {
		errs = append(errs, err)
	} else {
		s.Metrics[protocol.MetricDiskPercent] = du.UsedPercent
	}

	if la, err := load.AvgWithContext(ctx); err != nil {
		errs = append(errs, err)
	} else {
		s.Metrics[protocol.MetricLoad1] = la.Load1
	}

	if info, err := host.InfoWithContext(ctx); err != nil {
		errs = append(errs, err)
	} else {
		s.Metrics[protocol.MetricUptimeSeconds] = float64(info.Uptime)
	}

	// Temperature is best-effort: many hosts (containers, some VMs) expose
	// no sensors at all, and that is not a collection failure.
	if temps, err := sensors.TemperaturesWithContext(ctx); err == nil && len(temps) > 0 {
		max, found := 0.0, false
		for _, ts := range temps {
			if ts.Temperature <= 0 || ts.Temperature > sensibleTempMax {
				continue
			}
			if !found || ts.Temperature > max {
				max, found = ts.Temperature, true
			}
		}
		if found {
			s.Metrics[protocol.MetricTempC] = max
		}
	}

	if len(errs) > 0 {
		s.Error = errors.Join(errs...).Error()
	}
	if len(s.Metrics) == 0 {
		s.State = protocol.StateUnknown
	} else {
		s.State = protocol.StateHealthy
	}

	return s
}
