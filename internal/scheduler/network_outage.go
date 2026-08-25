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
// forgetting the confirmed state of any website still marked down so its
// next real observation starts a fresh, honest confirmation.
//
// The fold-away on the way in exists because targets don't all confirm down
// in the same instant: with two sites on independent check intervals, the
// first to confirm opens its own incident normally, since outage isn't
// true yet with only one site down. Once the second confirms and outage
// becomes true, leaving that first incident open would be exactly the
// manufactured, misattributed incident this whole check exists to prevent,
// so it is resolved here in favour of the single network incident. This is
// safe precisely because isNetworkOutage requires every website target to
// be confirmed Down before outage is ever true: an open per-site incident
// for a website in Warning (a certificate expiring, say, not a failure)
// would already have kept outage false, so every open website incident
// reachable from this branch is a Down incident being folded, never a
// different kind of problem being silently erased. It checks whether the
// network incident is already open before doing any of this, so a network
// outage that persists across many later transitions (other targets still
// ticking, host metrics wobbling) does the fold-away store writes once per
// outage, not once per transition.
//
// Symmetrically, the cleanup on the way out (resolving the network incident
// and forgetting each website's confirmed state) only runs when a network
// incident was actually open to clean up. Without that guard, this would
// also run on the far more common case of outage simply never having been
// true, for example the first website to go down while its siblings
// haven't been checked yet: that target's Current() is Down too, and
// forgetting it there would erase the very confirmation this call is in
// the middle of processing, before its own incident ever gets a chance to
// open. Checking the store for whether the network incident actually
// exists, rather than caching that fact in memory, also means a hub
// restart mid-outage reconciles correctly from what's on disk instead of
// from an assumption that resets to "no outage" on every process start.
//
// The reason the way out is a Forget rather than a backfill for the
// websites still marked down: Current() being Down does not mean the site
// is still genuinely broken. It also, and on a real recovery almost
// always, means the site simply has not been asked anything since the
// network came back, because targets tick on independent intervals.
// Opening an incident on that basis manufactures a false one for every
// target slower to be re-checked than whichever target happened to trigger
// this reconciliation, which is the exact pile-up this feature exists to
// prevent, just moved from the outage edge to the recovery edge. Forgetting
// resets the target to StateUnknown instead, so its next real observation
// goes through the ordinary path with no special case: if it is still
// genuinely down, it confirms Down from Unknown in OpenConfirmations
// samples and opens its own incident with its own summary; if it has
// actually recovered, it confirms Healthy instead and opens nothing.
// Either way the incident, if any, starts when the target is actually
// observed to be down, not at a guessed moment this function cannot
// support evidence for.
func (s *Scheduler) reconcileNetworkOutage(ctx context.Context, targets []protocol.Target, outage bool, at time.Time) {
	open, err := s.deps.Store.OpenIncidents(ctx)
	if err != nil {
		s.logger.Error("scheduler: list open incidents for network outage reconciliation", "err", err)
		return
	}
	networkOpen := false
	for _, in := range open {
		if in.TargetID == networkOutageTargetID {
			networkOpen = true
			break
		}
	}

	if outage {
		if networkOpen {
			return
		}
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

	if !networkOpen {
		return
	}

	if err := s.deps.Store.ResolveIncident(ctx, networkOutageTargetID, at); err != nil {
		s.logger.Error("scheduler: resolve network outage incident", "err", err)
	}
	for _, wt := range targets {
		if wt.Kind == protocol.KindWebsite && s.deps.Machine.Current(wt.ID) == protocol.StateDown {
			s.deps.Machine.Forget(wt.ID)
		}
	}
}
