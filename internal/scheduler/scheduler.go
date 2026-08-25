// Package scheduler runs Beacon's check loop: one goroutine per enabled
// target, ticking at that target's interval, collecting a sample,
// evaluating it against thresholds, persisting it, and feeding the result
// into the incident state machine.
package scheduler

import (
	"context"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/levimackay/beacon/internal/clock"
	"github.com/levimackay/beacon/internal/collect"
	"github.com/levimackay/beacon/internal/incident"
	"github.com/levimackay/beacon/internal/protocol"
	"github.com/levimackay/beacon/internal/store"
)

// refreshInterval is how often Run re-reads the target list from the store
// to start, stop and restart per-target goroutines.
//
// It is deliberately short. It is also the delay a user sees between adding
// a target and that target showing anything at all, and "I added my site
// and Beacon showed nothing for half a minute" reads as broken. Re-reading
// a table of a few dozen rows from a local SQLite file is far cheaper than
// that impression.
const refreshInterval = 5 * time.Second

// rollupInterval is how often Store.Rollup runs.
const rollupInterval = time.Hour

// maxJitterFraction is the share of a target's interval its first tick may
// be delayed by, so targets sharing an interval don't all fire in lockstep.
const maxJitterFraction = 0.10

// Deps are the Scheduler's dependencies.
type Deps struct {
	Store      store.Store
	Clock      clock.Clock
	Collectors map[protocol.TargetKind]collect.Collector
	Machine    *incident.Machine
	Thresholds incident.Thresholds
	Logger     *slog.Logger // may be nil -> discard

	// Sleep, if non-nil, replaces the scheduler's internal wait (jitter,
	// per-target interval, the 30s refresh tick, and the hourly rollup
	// tick). It reports whether the wait completed (true) or was cut
	// short by ctx being done (false). Tests inject this to drive the
	// loop deterministically without real wall-clock waits.
	Sleep func(ctx context.Context, d time.Duration) bool
}

// targetRun is a live per-target goroutine and the target definition it was
// started with, so a changed definition can be detected on refresh.
type targetRun struct {
	cancel context.CancelFunc
	target protocol.Target
}

// Scheduler runs the check loop described in the package doc.
type Scheduler struct {
	deps     Deps
	logger   *slog.Logger
	lastTick atomic.Int64 // UnixNano; 0 means "never".
}

// New builds a Scheduler from its dependencies.
func New(d Deps) *Scheduler {
	logger := d.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Scheduler{deps: d, logger: logger}
}

// LastTick reports the time of the most recently completed check, across
// all targets. Safe for concurrent read.
func (s *Scheduler) LastTick() time.Time {
	n := s.lastTick.Load()
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}

// Run drives the check loop until ctx is cancelled. It starts one goroutine
// per enabled target, re-reading the target list every 30s to start/stop
// goroutines as targets are added, removed, enabled or disabled, plus a
// goroutine that runs Store.Rollup hourly. It returns nil once ctx is done
// and every goroutine it started has stopped; no goroutine outlives Run.
func (s *Scheduler) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	running := map[string]*targetRun{} // touched only from this goroutine

	refresh := func() {
		targets, err := s.deps.Store.Targets(ctx)
		if err != nil {
			s.logger.Error("scheduler: list targets", "err", err)
			return
		}

		seen := make(map[string]bool, len(targets))
		for _, t := range targets {
			seen[t.ID] = true
			current, alreadyRunning := running[t.ID]

			if !t.Enabled {
				if alreadyRunning {
					current.cancel()
					delete(running, t.ID)
				}
				continue
			}

			// A running goroutine holds its own copy of the target, so
			// an edit to the interval, the URL, the expected status or
			// the private-network opt-in would otherwise never take
			// effect: the old goroutine would keep checking the old
			// definition until the target was deleted. Restart it when
			// the stored definition no longer matches.
			if alreadyRunning {
				if current.target == t {
					continue
				}
				current.cancel()
				delete(running, t.ID)
			}

			tctx, cancel := context.WithCancel(ctx)
			running[t.ID] = &targetRun{cancel: cancel, target: t}
			wg.Add(1)
			go s.runTarget(tctx, &wg, t)
		}

		for id, r := range running {
			if !seen[id] {
				r.cancel()
				delete(running, id)
				s.deps.Machine.Forget(id)
			}
		}
	}

	refresh()

	wg.Add(1)
	go s.runRollup(ctx, &wg)

	for s.sleepFor(ctx, refreshInterval) {
		refresh()
	}

	wg.Wait()
	return nil
}

// runTarget is the per-target loop: an initial jittered wait, then check,
// wait an interval, repeat, until ctx is done.
func (s *Scheduler) runTarget(ctx context.Context, wg *sync.WaitGroup, t protocol.Target) {
	defer wg.Done()

	if !s.sleepFor(ctx, jitter(t.ID, t.Interval())) {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		s.CheckOnce(ctx, t)
		if !s.sleepFor(ctx, t.Interval()) {
			return
		}
	}
}

func (s *Scheduler) runRollup(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	for s.sleepFor(ctx, rollupInterval) {
		if _, err := s.deps.Store.Rollup(ctx, s.deps.Clock.Now()); err != nil {
			s.logger.Error("scheduler: rollup", "err", err)
		}
	}
}

// sleepFor waits d via the injected Sleep hook (or a real timer), then
// re-checks ctx itself so a custom Sleep that ignores cancellation still
// can't make a goroutine outlive Run by more than one wait.
func (s *Scheduler) sleepFor(ctx context.Context, d time.Duration) bool {
	sleep := s.deps.Sleep
	if sleep == nil {
		sleep = defaultSleep
	}
	if !sleep(ctx, d) {
		return false
	}
	select {
	case <-ctx.Done():
		return false
	default:
		return true
	}
}

func defaultSleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// jitter derives a deterministic delay in [0, maxJitterFraction*interval)
// from the target's ID, so repeated runs (and tests) are reproducible and
// no global math/rand source is needed.
func jitter(targetID string, interval time.Duration) time.Duration {
	h := fnv.New32a()
	_, _ = h.Write([]byte(targetID))
	frac := float64(h.Sum32()) / float64(^uint32(0))
	return time.Duration(frac * maxJitterFraction * float64(interval))
}

// CheckOnce runs one check cycle for t: collect, evaluate against
// thresholds, persist the sample, and feed the result to the incident
// machine, opening or resolving an incident on a confirmed transition. It
// never panics and never returns an error; failures are logged. Exported so
// the CLI's `beacon check` and tests can drive a single cycle directly.
func (s *Scheduler) CheckOnce(ctx context.Context, t protocol.Target) protocol.Sample {
	now := s.deps.Clock.Now()
	defer s.lastTick.Store(now.UnixNano())

	sample := s.collect(ctx, t, now)

	state, summary := s.deps.Thresholds.Evaluate(sample)
	sample.State = state
	if summary != "" {
		sample.Error = summary
	}

	if err := s.deps.Store.InsertSample(ctx, sample); err != nil {
		s.logger.Error("scheduler: insert sample", "target", t.ID, "err", err)
	}

	tr := s.deps.Machine.Observe(t.ID, sample.State, summary, sample.At)
	s.applyTransition(ctx, t, tr)

	return sample
}

// collect runs the target's collector and normalises the result: a missing
// collector or a collector panic both become a StateUnknown sample instead
// of crashing the scheduler.
func (s *Scheduler) collect(ctx context.Context, t protocol.Target, now time.Time) (sample protocol.Sample) {
	c, ok := s.deps.Collectors[t.Kind]
	if !ok {
		return protocol.Sample{
			TargetID: t.ID,
			At:       now,
			State:    protocol.StateUnknown,
			Error:    fmt.Sprintf("no collector registered for target kind %q", t.Kind),
		}
	}

	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("scheduler: collector panic", "target", t.ID, "panic", r)
			sample = protocol.Sample{
				TargetID: t.ID,
				At:       now,
				State:    protocol.StateUnknown,
				Error:    fmt.Sprintf("collector panic: %v", r),
			}
		}
	}()

	sample = c.Collect(ctx, t)
	sample.TargetID = t.ID
	if sample.At.IsZero() {
		sample.At = now
	}
	return sample
}

func (s *Scheduler) applyTransition(ctx context.Context, t protocol.Target, tr *incident.Transition) {
	if tr == nil {
		return
	}
	if tr.To == protocol.StateHealthy {
		if err := s.deps.Store.ResolveIncident(ctx, t.ID, tr.At); err != nil {
			s.logger.Error("scheduler: resolve incident", "target", t.ID, "err", err)
		}
		return
	}
	inc := protocol.Incident{
		TargetID:   t.ID,
		TargetName: t.Name,
		State:      tr.To,
		StartedAt:  tr.At,
		Summary:    tr.Summary,
	}
	if _, err := s.deps.Store.OpenIncident(ctx, inc); err != nil {
		s.logger.Error("scheduler: open incident", "target", t.ID, "err", err)
	}
}
