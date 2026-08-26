package scheduler_test

import (
	"testing"

	"github.com/levimackay/beacon/internal/collect"
	"github.com/levimackay/beacon/internal/protocol"
	"github.com/levimackay/beacon/internal/scheduler"
)

// isNetworkOutage treats KindHost and KindService identically as the local
// control. Every existing network-outage test uses a host; none exercise a
// service target alone proving the machine's local checks still work.
func TestCheckOnce_NetworkOutage_ServiceTargetAlsoCountsAsLocalControl(t *testing.T) {
	fc := newFakeClock()
	svc := healthyTarget("svc-1", protocol.KindService)
	web1 := healthyTarget("web-1", protocol.KindWebsite)
	web2 := healthyTarget("web-2", protocol.KindWebsite)
	st := newFakeStore(svc, web1, web2)

	svcC := newFakeCollector(func(protocol.Target) protocol.Sample { return protocol.Sample{State: protocol.StateHealthy} })
	webC := newFakeCollector(func(protocol.Target) protocol.Sample { return protocol.Sample{State: protocol.StateDown} })
	sched := scheduler.New(newDeps(st, fc, map[protocol.TargetKind]collect.Collector{
		protocol.KindService: svcC,
		protocol.KindWebsite: webC,
	}))

	confirmHealthy(t, sched, svc)
	confirmDown(t, sched, web1)
	confirmDown(t, sched, web2)

	open, networkOpen := 0, false
	for _, in := range st.allIncidents() {
		if !in.Open() {
			continue
		}
		open++
		if in.TargetID == "beacon-network-outage" {
			networkOpen = true
		}
	}
	if open != 1 || !networkOpen {
		t.Fatalf("open incidents = %+v, want exactly one, against the network, with a healthy service as the local control", st.allIncidents())
	}
}

// A host or service target that exists but is not itself confirmed healthy
// (still down, or never yet confirmed) is not a valid control: there is
// nothing proving the machine's local checks work, so every failing website
// must still raise its own incident rather than being folded into a
// network verdict.
func TestCheckOnce_NetworkOutage_UnhealthyLocalTargetNeverSuppresses(t *testing.T) {
	fc := newFakeClock()
	host := healthyTarget("host-1", protocol.KindHost)
	web1 := healthyTarget("web-1", protocol.KindWebsite)
	web2 := healthyTarget("web-2", protocol.KindWebsite)
	st := newFakeStore(host, web1, web2)

	hostC := newFakeCollector(func(protocol.Target) protocol.Sample {
		return protocol.Sample{State: protocol.StateDown, Error: "host itself is down"}
	})
	webC := newFakeCollector(func(protocol.Target) protocol.Sample { return protocol.Sample{State: protocol.StateDown} })
	sched := scheduler.New(newDeps(st, fc, map[protocol.TargetKind]collect.Collector{
		protocol.KindHost:    hostC,
		protocol.KindWebsite: webC,
	}))

	confirmDown(t, sched, host)
	confirmDown(t, sched, web1)
	confirmDown(t, sched, web2)

	got := st.allIncidents()
	for _, in := range got {
		if in.TargetID == "beacon-network-outage" {
			t.Fatalf("network incident raised with no healthy local control (host itself is down): %+v", got)
		}
	}
	openWebsites := 0
	for _, in := range got {
		if in.Open() && (in.TargetID == "web-1" || in.TargetID == "web-2") {
			openWebsites++
		}
	}
	if openWebsites != 2 {
		t.Fatalf("open per-site incidents = %d, want 2 (a down host is not a valid local control)", openWebsites)
	}
}

// A disabled website target must not inflate the denominator isNetworkOutage
// uses: if it wrongly counted, an unconfirmed (never-checked, disabled)
// third site would make "every website down" false and block the network
// verdict that the two enabled, genuinely down sites should produce.
func TestCheckOnce_NetworkOutage_DisabledWebsiteExcludedFromCount(t *testing.T) {
	fc := newFakeClock()
	host := healthyTarget("host-1", protocol.KindHost)
	web1 := healthyTarget("web-1", protocol.KindWebsite)
	web2 := healthyTarget("web-2", protocol.KindWebsite)
	web3 := protocol.Target{ID: "web-3", Kind: protocol.KindWebsite, Name: "n-web-3", IntervalSeconds: 5, Enabled: false}
	st := newFakeStore(host, web1, web2, web3)

	hostC := newFakeCollector(func(protocol.Target) protocol.Sample { return protocol.Sample{State: protocol.StateHealthy} })
	webC := newFakeCollector(func(protocol.Target) protocol.Sample { return protocol.Sample{State: protocol.StateDown} })
	sched := scheduler.New(newDeps(st, fc, map[protocol.TargetKind]collect.Collector{
		protocol.KindHost:    hostC,
		protocol.KindWebsite: webC,
	}))

	confirmHealthy(t, sched, host)
	confirmDown(t, sched, web1)
	confirmDown(t, sched, web2)
	// web-3 is disabled and deliberately never checked, matching how the
	// real scheduler would never start a goroutine for it.

	networkOpen := false
	for _, in := range st.allIncidents() {
		if in.TargetID == "beacon-network-outage" && in.Open() {
			networkOpen = true
		}
	}
	if !networkOpen {
		t.Fatalf("incidents = %+v, want a network incident from the 2 enabled sites, unaffected by the disabled third", st.allIncidents())
	}
}

// The mirror of the disabled-website case: a disabled host must not count
// as the local control any more than an absent one would (see
// TestCheckOnce_NetworkOutage_NoLocalTargetNeverSuppressed).
func TestCheckOnce_NetworkOutage_DisabledLocalTargetNeverSuppresses(t *testing.T) {
	fc := newFakeClock()
	host := protocol.Target{ID: "host-1", Kind: protocol.KindHost, Name: "n-host-1", IntervalSeconds: 5, Enabled: false}
	web1 := healthyTarget("web-1", protocol.KindWebsite)
	web2 := healthyTarget("web-2", protocol.KindWebsite)
	st := newFakeStore(host, web1, web2)

	hostC := newFakeCollector(func(protocol.Target) protocol.Sample { return protocol.Sample{State: protocol.StateHealthy} })
	webC := newFakeCollector(func(protocol.Target) protocol.Sample { return protocol.Sample{State: protocol.StateDown} })
	sched := scheduler.New(newDeps(st, fc, map[protocol.TargetKind]collect.Collector{
		protocol.KindHost:    hostC,
		protocol.KindWebsite: webC,
	}))

	// CheckOnce does not consult Enabled itself (only Run's refresh loop
	// does), so this call still lets the Machine confirm the host healthy;
	// isNetworkOutage must still exclude it because the stored target says
	// Enabled: false.
	confirmHealthy(t, sched, host)
	confirmDown(t, sched, web1)
	confirmDown(t, sched, web2)

	got := st.allIncidents()
	for _, in := range got {
		if in.TargetID == "beacon-network-outage" {
			t.Fatalf("network incident raised using a disabled host as the local control: %+v", got)
		}
	}
}
