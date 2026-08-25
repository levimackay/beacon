package scheduler

import (
	"context"
	"time"

	"github.com/levimackay/beacon/internal/protocol"
)

// networkOutageTargetID is the id incidents are filed under when Beacon
// concludes its own path to the internet is down, rather than any one
// website it watches. It is not a real target: there is nothing to add,
// edit or delete under this id, only incidents. It is safe as a fixed
// constant because real target ids are random hex (internal/api/targets.go
// randomID), and this string is neither hex nor 16 characters, so it can
// never collide with one.
const networkOutageTargetID = "beacon-network-outage"

const networkOutageTargetName = "Network"

const networkOutageSummary = "every website target is unreachable while local checks stay healthy: " +
	"likely this machine's own network connection, not the sites"

// isNetworkOutage decides whether the reason every failing website target
// is failing is that this machine cannot currently reach the internet at
// all, rather than those sites themselves being broken.
//
// The evidence: every enabled website target is confirmed down, and at
// least one enabled host or service target is confirmed healthy. Host and
// service checks (CPU, memory, disk, launchd/systemd unit state) never
// leave the machine Beacon runs on, so one of them succeeding proves the
// collector process and the machine itself are working normally. If every
// remote check fails at the same time that a purely local check keeps
// succeeding, "this machine's uplink is down" explains all of it; "every
// independently hosted site happened to break at the same moment" does not,
// and gets less likely the more sites there are.
//
// Two things stop this from hiding a real multi-site outage:
//
//   - It requires every website target down, not most of them. One target
//     that is still reachable is itself proof the network path out of this
//     machine works, which rules out a network explanation immediately and
//     correctly leaves the down targets to raise their own incidents.
//   - It requires an actually-healthy local target as the control. No host
//     or service target, or one that is itself down or was never confirmed
//     healthy, means there is nothing to compare the website failures
//     against, so this reports no outage and every site raises its own
//     incident as usual. Missing the chance to merge several incidents
//     into one is an acceptable cost; manufacturing a network excuse for a
//     real outage is not.
//
// It also requires at least two website targets. With exactly one, "the
// site is down" and "the network is down" look identical, and there is no
// second target to break the tie.
func isNetworkOutage(targets []protocol.Target, current func(targetID string) protocol.State) bool {
	websiteCount := 0
	allWebsitesDown := true
	localHealthy := false

	for _, t := range targets {
		if !t.Enabled {
			continue
		}
		switch t.Kind {
		case protocol.KindWebsite:
			websiteCount++
			if current(t.ID) != protocol.StateDown {
				allWebsitesDown = false
			}
		case protocol.KindHost, protocol.KindService:
			if current(t.ID) == protocol.StateHealthy {
				localHealthy = true
			}
		}
	}

	return websiteCount >= 2 && allWebsitesDown && localHealthy
}

// reconcileNetworkOutage keeps the synthetic network incident in sync with
// outage: opening (or keeping open) the one network incident and folding
// away any per-site incidents it now explains, or, once the outage clears,
// opening the incident for any website that is still confirmed down for
// its own reasons.
//
// The fold-away on the way in exists because targets don't all confirm
// down in the same instant: with two sites on independent check intervals,
// the first to confirm opens its own incident normally, since outage isn't
// true yet with only one site down. Once the second confirms and outage
// becomes true, leaving that first incident open would be exactly the
// manufactured, misattributed incident this whole check exists to prevent,
// so it is resolved here in favour of the single network incident. This is
// safe precisely because isNetworkOutage requires every website target to
// be confirmed Down before outage is ever true: an open per-site incident
// for a website in Warning (a certificate expiring, say, not a failure)
// would already have kept outage false, so every open website incident
// reachable from this branch is a Down incident being folded, never a
// different kind of problem being silently erased.
//
// The backfill on the way out exists for the same underlying reason in
// reverse: a website's down transition is never given its own incident
// while outage is true, so if that website is still down once the network
// explanation stops applying, it never otherwise gets an incident of its
// own, because the state machine only emits a Transition on a change, and
// this target's change to Down already happened and was consumed.
// OpenIncident is a no-op for a target that already has one open, so
// running this check on every reconciliation costs nothing on the vastly
// more common case where nothing was ever suppressed.
//
// The backfilled incident's start time is the moment of this reconciliation,
// not whenever the site actually first failed: while the network explanation
// held, Beacon had no way to tell "down because of the network the whole
// time" from "down for its own reasons partway through," and it would rather
// under-report an incident's duration than invent a start time it cannot
// support.
func (s *Scheduler) reconcileNetworkOutage(ctx context.Context, targets []protocol.Target, outage bool, at time.Time) {
	if outage {
		inc := protocol.Incident{
			TargetID:   networkOutageTargetID,
			TargetName: networkOutageTargetName,
			State:      protocol.StateDown,
			StartedAt:  at,
			Summary:    networkOutageSummary,
		}
		if _, err := s.deps.Store.OpenIncident(ctx, inc); err != nil {
			s.logger.Error("scheduler: open network outage incident", "err", err)
		}
		for _, wt := range targets {
			if wt.Kind != protocol.KindWebsite {
				continue
			}
			if err := s.deps.Store.ResolveIncident(ctx, wt.ID, at); err != nil {
				s.logger.Error("scheduler: fold per-site incident into network outage", "target", wt.ID, "err", err)
			}
		}
		return
	}

	if err := s.deps.Store.ResolveIncident(ctx, networkOutageTargetID, at); err != nil {
		s.logger.Error("scheduler: resolve network outage incident", "err", err)
	}

	var samples map[string]protocol.Sample
	for _, wt := range targets {
		if wt.Kind != protocol.KindWebsite || s.deps.Machine.Current(wt.ID) != protocol.StateDown {
			continue
		}
		if samples == nil {
			var err error
			samples, err = s.deps.Store.LatestSamples(ctx)
			if err != nil {
				s.logger.Error("scheduler: latest samples for network-outage backfill", "err", err)
				samples = map[string]protocol.Sample{}
			}
		}
		inc := protocol.Incident{
			TargetID:   wt.ID,
			TargetName: wt.Name,
			State:      protocol.StateDown,
			StartedAt:  at,
			Summary:    samples[wt.ID].Error,
		}
		if _, err := s.deps.Store.OpenIncident(ctx, inc); err != nil {
			s.logger.Error("scheduler: open incident after network recovery", "target", wt.ID, "err", err)
		}
	}
}
