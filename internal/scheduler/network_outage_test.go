package scheduler_test

import (
	"context"
	"testing"

	"github.com/levimackay/beacon/internal/collect"
	"github.com/levimackay/beacon/internal/protocol"
	"github.com/levimackay/beacon/internal/scheduler"
)

// stateFor lets a fakeCollector return a different, mutable state per
// target id, keyed by the id rather than fixed at construction, so a test
// can drive several targets of the same kind independently.
func stateFor(states map[string]protocol.State) func(protocol.Target) protocol.Sample {
	return func(t protocol.Target) protocol.Sample {
		return protocol.Sample{State: states[t.ID], Error: "unreachable"}
	}
}

func confirmDown(t *testing.T, sched *scheduler.Scheduler, tgt protocol.Target) {
	t.Helper()
	sched.CheckOnce(context.Background(), tgt)
	sched.CheckOnce(context.Background(), tgt)
}

func confirmHealthy(t *testing.T, sched *scheduler.Scheduler, tgt protocol.Target) {
	t.Helper()
	sched.CheckOnce(context.Background(), tgt)
	sched.CheckOnce(context.Background(), tgt)
}

// The central case: every website target confirms down while the host
// target stays confirmed healthy. Beacon must raise exactly one incident,
// against the network, not one against each site.
func TestCheckOnce_NetworkOutage_OnePerSiteIncidentsNotSuppressedUntilAllDown(t *testing.T) {
	fc := newFakeClock()
	host := healthyTarget("host-1", protocol.KindHost)
	web1 := healthyTarget("web-1", protocol.KindWebsite)
	web2 := healthyTarget("web-2", protocol.KindWebsite)
	st := newFakeStore(host, web1, web2)

	states := map[string]protocol.State{"web-1": protocol.StateDown, "web-2": protocol.StateHealthy}
	hostC := newFakeCollector(func(protocol.Target) protocol.Sample { return protocol.Sample{State: protocol.StateHealthy} })
	webC := newFakeCollector(stateFor(states))
	sched := scheduler.New(newDeps(st, fc, map[protocol.TargetKind]collect.Collector{
		protocol.KindHost:    hostC,
		protocol.KindWebsite: webC,
	}))

	confirmHealthy(t, sched, host)
	confirmDown(t, sched, web1)

	// Only one of two website targets is down: not a network outage, so
	// web-1 gets its own ordinary incident.
	got := st.allIncidents()
	if len(got) != 1 || got[0].TargetID != "web-1" || !got[0].Open() {
		t.Fatalf("with one site down: incidents = %+v, want exactly one open incident against web-1", got)
	}

	// Now web-2 also goes down: every website target is down, and the host
	// stays confirmed healthy. This is the outage condition.
	states["web-2"] = protocol.StateDown
	confirmDown(t, sched, web2)

	got = st.allIncidents()
	var network *protocol.Incident
	for i := range got {
		if got[i].TargetID == "beacon-network-outage" {
			network = &got[i]
		}
		if got[i].TargetID == "web-2" {
			t.Fatalf("web-2 got its own incident %+v, want it folded into the network incident", got[i])
		}
	}
	if network == nil || !network.Open() {
		t.Fatalf("incidents = %+v, want an open network incident", got)
	}

	// web-1's earlier per-site incident must be folded away (resolved) once
	// the network explanation covers it: leaving it open would be exactly
	// the manufactured false incident this check exists to prevent.
	for _, in := range got {
		if in.TargetID == "web-1" && in.Open() {
			t.Fatalf("web-1's per-site incident is still open: %+v, want it resolved in favour of the network incident", in)
		}
	}
}

// A real outage across several sites, none of which is a network problem,
// must still raise its own incidents: the host is healthy, but not every
// website target is down.
func TestCheckOnce_NetworkOutage_PartialOutageStillRaisesPerSiteIncidents(t *testing.T) {
	fc := newFakeClock()
	host := healthyTarget("host-1", protocol.KindHost)
	web1 := healthyTarget("web-1", protocol.KindWebsite)
	web2 := healthyTarget("web-2", protocol.KindWebsite)
	web3 := healthyTarget("web-3", protocol.KindWebsite)
	st := newFakeStore(host, web1, web2, web3)

	states := map[string]protocol.State{
		"web-1": protocol.StateDown,
		"web-2": protocol.StateDown,
		"web-3": protocol.StateHealthy, // this one is fine: proves the network is up
	}
	hostC := newFakeCollector(func(protocol.Target) protocol.Sample { return protocol.Sample{State: protocol.StateHealthy} })
	webC := newFakeCollector(stateFor(states))
	sched := scheduler.New(newDeps(st, fc, map[protocol.TargetKind]collect.Collector{
		protocol.KindHost:    hostC,
		protocol.KindWebsite: webC,
	}))

	confirmHealthy(t, sched, host)
	confirmHealthy(t, sched, web3)
	confirmDown(t, sched, web1)
	confirmDown(t, sched, web2)

	got := st.allIncidents()
	if len(got) != 2 {
		t.Fatalf("incidents = %+v, want exactly 2 (web-1 and web-2), no network incident", got)
	}
	for _, in := range got {
		if in.TargetID == "beacon-network-outage" {
			t.Fatalf("a network incident was raised for a genuine partial outage: %+v", got)
		}
		if !in.Open() {
			t.Fatalf("incident %+v should still be open", in)
		}
	}
}

// With only one website target configured, "the site is down" and "the
// network is down" are indistinguishable, so Beacon must not guess: it
// raises the site's own incident rather than inventing a network one.
func TestCheckOnce_NetworkOutage_SingleWebsiteNeverSuppressed(t *testing.T) {
	fc := newFakeClock()
	host := healthyTarget("host-1", protocol.KindHost)
	web1 := healthyTarget("web-1", protocol.KindWebsite)
	st := newFakeStore(host, web1)

	hostC := newFakeCollector(func(protocol.Target) protocol.Sample { return protocol.Sample{State: protocol.StateHealthy} })
	webC := newFakeCollector(func(protocol.Target) protocol.Sample { return protocol.Sample{State: protocol.StateDown} })
	sched := scheduler.New(newDeps(st, fc, map[protocol.TargetKind]collect.Collector{
		protocol.KindHost:    hostC,
		protocol.KindWebsite: webC,
	}))

	confirmHealthy(t, sched, host)
	confirmDown(t, sched, web1)

	got := st.allIncidents()
	if len(got) != 1 || got[0].TargetID != "web-1" {
		t.Fatalf("incidents = %+v, want exactly one incident against web-1", got)
	}
}

// With no host or service target at all, there is no local control to
// compare the website failures against, so Beacon must not guess "network"
// even when every website target happens to be down.
func TestCheckOnce_NetworkOutage_NoLocalTargetNeverSuppressed(t *testing.T) {
	fc := newFakeClock()
	web1 := healthyTarget("web-1", protocol.KindWebsite)
	web2 := healthyTarget("web-2", protocol.KindWebsite)
	st := newFakeStore(web1, web2)

	webC := newFakeCollector(func(protocol.Target) protocol.Sample { return protocol.Sample{State: protocol.StateDown} })
	sched := scheduler.New(newDeps(st, fc, map[protocol.TargetKind]collect.Collector{protocol.KindWebsite: webC}))

	confirmDown(t, sched, web1)
	confirmDown(t, sched, web2)

	got := st.allIncidents()
	if len(got) != 2 {
		t.Fatalf("incidents = %+v, want 2 per-site incidents (no local control target to justify a network verdict)", got)
	}
}

// Once the network recovers, a website that is still genuinely down (not
// just caught up in the outage) must get its own incident: the outage
// explanation stops covering it the moment it stops being universally true.
func TestCheckOnce_NetworkOutage_RecoveryBackfillsStillDownSite(t *testing.T) {
	fc := newFakeClock()
	host := healthyTarget("host-1", protocol.KindHost)
	web1 := healthyTarget("web-1", protocol.KindWebsite)
	web2 := healthyTarget("web-2", protocol.KindWebsite)
	st := newFakeStore(host, web1, web2)

	states := map[string]protocol.State{"web-1": protocol.StateDown, "web-2": protocol.StateDown}
	hostC := newFakeCollector(func(protocol.Target) protocol.Sample { return protocol.Sample{State: protocol.StateHealthy} })
	webC := newFakeCollector(stateFor(states))
	sched := scheduler.New(newDeps(st, fc, map[protocol.TargetKind]collect.Collector{
		protocol.KindHost:    hostC,
		protocol.KindWebsite: webC,
	}))

	confirmHealthy(t, sched, host)
	confirmDown(t, sched, web1)
	confirmDown(t, sched, web2)

	network := func() *protocol.Incident {
		for _, in := range st.allIncidents() {
			if in.TargetID == "beacon-network-outage" {
				return &in
			}
		}
		return nil
	}
	if in := network(); in == nil || !in.Open() {
		t.Fatalf("want an open network incident before recovery, got %+v", st.allIncidents())
	}

	// web-1 recovers, but web-2 is genuinely still broken on its own.
	// Recovering web-1 needs 3 confirmations (closing an incident), and
	// only after that does the network condition stop holding.
	states["web-1"] = protocol.StateHealthy
	for i := 0; i < 3; i++ {
		sched.CheckOnce(context.Background(), web1)
	}

	if in := network(); in != nil && in.Open() {
		t.Fatalf("network incident should be resolved once not every site is down, got %+v", *in)
	}

	found := false
	for _, in := range st.allIncidents() {
		if in.TargetID == "web-2" && in.Open() {
			found = true
		}
	}
	if !found {
		t.Fatalf("web-2 is still down and should have gotten its own incident once the network recovered, got %+v", st.allIncidents())
	}
}

// Rapid confirmed flapping across all website targets while the host stays
// healthy must still collapse to a single open network incident, not a
// storm of opens and closes: this is the flap-suppression guarantee and the
// network-outage guarantee working together, not one undoing the other.
func TestCheckOnce_NetworkOutage_DoesNotStormWhileFlapping(t *testing.T) {
	fc := newFakeClock()
	host := healthyTarget("host-1", protocol.KindHost)
	web1 := healthyTarget("web-1", protocol.KindWebsite)
	web2 := healthyTarget("web-2", protocol.KindWebsite)
	st := newFakeStore(host, web1, web2)

	states := map[string]protocol.State{"web-1": protocol.StateDown, "web-2": protocol.StateDown}
	hostC := newFakeCollector(func(protocol.Target) protocol.Sample { return protocol.Sample{State: protocol.StateHealthy} })
	webC := newFakeCollector(stateFor(states))
	sched := scheduler.New(newDeps(st, fc, map[protocol.TargetKind]collect.Collector{
		protocol.KindHost:    hostC,
		protocol.KindWebsite: webC,
	}))

	confirmHealthy(t, sched, host)
	confirmDown(t, sched, web1)
	confirmDown(t, sched, web2)

	// Bounce web-1 up for a single sample and back down repeatedly: a
	// single good sample never confirms recovery (OpenConfirmations for
	// the down side, CloseConfirmations for the healthy side, both > 1),
	// so this must never re-open or duplicate the network incident.
	for i := 0; i < 5; i++ {
		states["web-1"] = protocol.StateHealthy
		sched.CheckOnce(context.Background(), web1)
		states["web-1"] = protocol.StateDown
		sched.CheckOnce(context.Background(), web1)
	}

	open := 0
	for _, in := range st.allIncidents() {
		if in.Open() {
			open++
		}
	}
	if open != 1 {
		t.Fatalf("open incidents after flapping = %d, want exactly 1 (the network incident)", open)
	}
}
