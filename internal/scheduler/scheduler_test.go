package scheduler_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/levimackay/beacon/internal/clock"
	"github.com/levimackay/beacon/internal/collect"
	"github.com/levimackay/beacon/internal/incident"
	"github.com/levimackay/beacon/internal/protocol"
	"github.com/levimackay/beacon/internal/scheduler"
	"github.com/levimackay/beacon/internal/store"
)

// fakeStore is a minimal, mutex-protected store.Store for tests. Only the
// behaviour the scheduler actually depends on (targets, samples,
// incidents, rollup) is real; the rest are harmless stubs.
type fakeStore struct {
	mu        sync.Mutex
	targets   []protocol.Target
	samples   []protocol.Sample
	incidents []protocol.Incident
	nextID    int64
	rollups   int
}

func newFakeStore(targets ...protocol.Target) *fakeStore {
	return &fakeStore{targets: append([]protocol.Target(nil), targets...)}
}

func (f *fakeStore) UpsertTarget(_ context.Context, t protocol.Target) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, existing := range f.targets {
		if existing.ID == t.ID {
			f.targets[i] = t
			return nil
		}
	}
	f.targets = append(f.targets, t)
	return nil
}

func (f *fakeStore) DeleteTarget(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.targets[:0]
	for _, t := range f.targets {
		if t.ID != id {
			out = append(out, t)
		}
	}
	f.targets = out
	return nil
}

func (f *fakeStore) Targets(_ context.Context) ([]protocol.Target, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]protocol.Target(nil), f.targets...), nil
}

func (f *fakeStore) InsertSample(_ context.Context, s protocol.Sample) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.samples = append(f.samples, s)
	return nil
}

func (f *fakeStore) LatestSamples(_ context.Context) (map[string]protocol.Sample, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]protocol.Sample{}
	for _, s := range f.samples {
		out[s.TargetID] = s
	}
	return out, nil
}

func (f *fakeStore) SampleSeries(_ context.Context, _, _ string, _ time.Time) ([]store.Point, error) {
	return nil, nil
}

func (f *fakeStore) OpenIncident(_ context.Context, in protocol.Incident) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, existing := range f.incidents {
		if existing.TargetID == in.TargetID && existing.Open() {
			return f.incidents[i].ID, nil
		}
	}
	f.nextID++
	in.ID = f.nextID
	f.incidents = append(f.incidents, in)
	return in.ID, nil
}

func (f *fakeStore) ResolveIncident(_ context.Context, targetID string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, existing := range f.incidents {
		if existing.TargetID == targetID && existing.Open() {
			t := at
			f.incidents[i].ResolvedAt = &t
		}
	}
	return nil
}

func (f *fakeStore) OpenIncidents(_ context.Context) ([]protocol.Incident, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []protocol.Incident
	for _, in := range f.incidents {
		if in.Open() {
			out = append(out, in)
		}
	}
	return out, nil
}

func (f *fakeStore) Incidents(_ context.Context, _ store.IncidentFilter) ([]protocol.Incident, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]protocol.Incident(nil), f.incidents...), nil
}

func (f *fakeStore) Audit(context.Context, string, string, string, string) error { return nil }

func (f *fakeStore) AuditTail(context.Context, int) ([]store.AuditRow, error) { return nil, nil }

func (f *fakeStore) Rollup(context.Context, time.Time) (store.RollupStats, error) {
	f.mu.Lock()
	f.rollups++
	f.mu.Unlock()
	return store.RollupStats{}, nil
}

func (f *fakeStore) Stats(context.Context) (store.Stats, error) { return store.Stats{}, nil }

func (f *fakeStore) Close() error { return nil }

func (f *fakeStore) allIncidents() []protocol.Incident {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]protocol.Incident(nil), f.incidents...)
}

func (f *fakeStore) lastSample() protocol.Sample {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.samples[len(f.samples)-1]
}

func (f *fakeStore) sampleCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.samples)
}

// fakeCollector is a collect.Collector whose result is fully controlled by
// the test, and which records every call on a buffered channel so tests can
// wait for (or assert the absence of) collections without sleeping blind.
type fakeCollector struct {
	calls  chan protocol.Target
	result func(protocol.Target) protocol.Sample
}

func newFakeCollector(result func(protocol.Target) protocol.Sample) *fakeCollector {
	return &fakeCollector{calls: make(chan protocol.Target, 1000), result: result}
}

func (f *fakeCollector) Collect(_ context.Context, t protocol.Target) protocol.Sample {
	select {
	case f.calls <- t:
	default:
	}
	return f.result(t)
}

func healthyTarget(id string, kind protocol.TargetKind) protocol.Target {
	return protocol.Target{ID: id, Kind: kind, Name: "n-" + id, IntervalSeconds: 5, Enabled: true}
}

func newDeps(st store.Store, c clock.Clock, collectors map[protocol.TargetKind]collect.Collector) scheduler.Deps {
	return scheduler.Deps{
		Store:      st,
		Clock:      c,
		Collectors: collectors,
		Machine:    incident.NewMachine(c),
		Thresholds: incident.DefaultThresholds(),
	}
}

// --- CheckOnce: driven directly, single goroutine, fully deterministic. ---

func TestCheckOnce_TwoFailuresOpenExactlyOneIncident(t *testing.T) {
	fc := newFakeClock()
	st := newFakeStore()
	down := newFakeCollector(func(target protocol.Target) protocol.Sample {
		return protocol.Sample{State: protocol.StateDown, Error: "connection refused"}
	})
	sched := scheduler.New(newDeps(st, fc, map[protocol.TargetKind]collect.Collector{protocol.KindHost: down}))
	tgt := healthyTarget("t1", protocol.KindHost)

	sched.CheckOnce(context.Background(), tgt)
	if got := st.allIncidents(); len(got) != 0 {
		t.Fatalf("after 1 failure: got %d incidents, want 0", len(got))
	}

	sched.CheckOnce(context.Background(), tgt)
	got := st.allIncidents()
	if len(got) != 1 {
		t.Fatalf("after 2 failures: got %d incidents, want 1", len(got))
	}
	if !got[0].Open() {
		t.Fatalf("incident should still be open")
	}

	sched.CheckOnce(context.Background(), tgt)
	if got := st.allIncidents(); len(got) != 1 {
		t.Fatalf("after 3rd failure: got %d incidents, want still 1", len(got))
	}
}

func TestCheckOnce_TwoSuccessesResolveIncident(t *testing.T) {
	fc := newFakeClock()
	st := newFakeStore()
	state := protocol.StateDown
	c := newFakeCollector(func(protocol.Target) protocol.Sample {
		return protocol.Sample{State: state}
	})
	sched := scheduler.New(newDeps(st, fc, map[protocol.TargetKind]collect.Collector{protocol.KindHost: c}))
	tgt := healthyTarget("t1", protocol.KindHost)

	sched.CheckOnce(context.Background(), tgt)
	sched.CheckOnce(context.Background(), tgt)
	if got := st.allIncidents(); len(got) != 1 || got[0].Open() != true {
		t.Fatalf("setup: want exactly one open incident, got %+v", got)
	}

	state = protocol.StateHealthy
	fc.Advance(time.Minute)
	sched.CheckOnce(context.Background(), tgt)
	if got := st.allIncidents(); len(got) != 1 || !got[0].Open() {
		t.Fatalf("after 1 success: incident should still be open, got %+v", got)
	}

	fc.Advance(time.Minute)
	sched.CheckOnce(context.Background(), tgt)
	got := st.allIncidents()
	if len(got) != 1 {
		t.Fatalf("got %d incidents, want 1", len(got))
	}
	if got[0].Open() {
		t.Fatalf("incident should be resolved")
	}
	if got[0].ResolvedAt.Before(got[0].StartedAt) {
		t.Fatalf("resolved before started: %+v", got[0])
	}

	// A further healthy check must not resolve (or reopen) anything again.
	fc.Advance(time.Minute)
	sched.CheckOnce(context.Background(), tgt)
	if got := st.allIncidents(); len(got) != 1 {
		t.Fatalf("extra healthy check changed incident count: %+v", got)
	}
}

func TestCheckOnce_PanickingCollectorRecovers(t *testing.T) {
	fc := newFakeClock()
	st := newFakeStore()
	calls := 0
	c := newFakeCollector(func(protocol.Target) protocol.Sample {
		calls++
		if calls == 1 {
			panic("boom")
		}
		return protocol.Sample{State: protocol.StateHealthy}
	})
	sched := scheduler.New(newDeps(st, fc, map[protocol.TargetKind]collect.Collector{protocol.KindHost: c}))
	tgt := healthyTarget("t1", protocol.KindHost)

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("CheckOnce must recover from a collector panic, got %v", r)
			}
		}()
		sched.CheckOnce(context.Background(), tgt)
	}()

	s := st.lastSample()
	if s.State != protocol.StateUnknown {
		t.Fatalf("state after panic = %v, want unknown", s.State)
	}
	if s.Error == "" {
		t.Fatalf("expected the panic to be described in the sample's error")
	}

	// The scheduler must keep working afterwards.
	sched.CheckOnce(context.Background(), tgt)
	if got := st.lastSample().State; got != protocol.StateHealthy {
		t.Fatalf("state after recovery = %v, want healthy", got)
	}
}

func TestCheckOnce_NoCollectorForKindIsUnknownNotPanic(t *testing.T) {
	fc := newFakeClock()
	st := newFakeStore()
	sched := scheduler.New(newDeps(st, fc, map[protocol.TargetKind]collect.Collector{}))
	tgt := healthyTarget("t1", protocol.KindService)

	sample := sched.CheckOnce(context.Background(), tgt)
	if sample.State != protocol.StateUnknown {
		t.Fatalf("state = %v, want unknown", sample.State)
	}
	if sample.Error == "" {
		t.Fatalf("expected an explanation for the missing collector")
	}
}

func TestCheckOnce_ThresholdsOverrideHealthyCollectorState(t *testing.T) {
	fc := newFakeClock()
	st := newFakeStore()
	c := newFakeCollector(func(protocol.Target) protocol.Sample {
		return protocol.Sample{
			State:   protocol.StateHealthy,
			Metrics: map[string]float64{protocol.MetricCPUPercent: 99},
		}
	})
	sched := scheduler.New(newDeps(st, fc, map[protocol.TargetKind]collect.Collector{protocol.KindHost: c}))
	tgt := healthyTarget("t1", protocol.KindHost)

	sample := sched.CheckOnce(context.Background(), tgt)
	if sample.State != protocol.StateDown {
		t.Fatalf("state = %v, want down (cpu_percent 99 breaches CPUDown=95)", sample.State)
	}
	if got := st.lastSample().State; got != protocol.StateDown {
		t.Fatalf("persisted state = %v, want down", got)
	}
}

// --- Run: exercises the concurrent goroutine-per-target loop. ---

// fastSleep replaces real interval/refresh/rollup waits with a short real
// wait, ignoring the requested duration, so Run-based tests complete in
// milliseconds instead of minutes/hours while still respecting ctx
// cancellation.
func fastSleep(ctx context.Context, _ time.Duration) bool {
	select {
	case <-time.After(2 * time.Millisecond):
		return true
	case <-ctx.Done():
		return false
	}
}

func waitForCall(t *testing.T, calls <-chan protocol.Target, timeout time.Duration) protocol.Target {
	t.Helper()
	select {
	case tgt := <-calls:
		return tgt
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for a collector call")
		return protocol.Target{}
	}
}

func assertNoCall(t *testing.T, calls <-chan protocol.Target, within time.Duration) {
	t.Helper()
	select {
	case tgt := <-calls:
		t.Fatalf("unexpected collector call for target %q", tgt.ID)
	case <-time.After(within):
	}
}

func runAndCancel(t *testing.T, sched *scheduler.Scheduler) (cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- sched.Run(ctx) }()
	return cancel, errCh
}

func TestRun_CancelStopsAllGoroutinesPromptly(t *testing.T) {
	st := newFakeStore(healthyTarget("t1", protocol.KindHost))
	c := newFakeCollector(func(protocol.Target) protocol.Sample { return protocol.Sample{State: protocol.StateHealthy} })
	deps := newDeps(st, clock.Real(), map[protocol.TargetKind]collect.Collector{protocol.KindHost: c})
	deps.Sleep = fastSleep
	sched := scheduler.New(deps)

	cancel, done := runAndCancel(t, sched)
	waitForCall(t, c.calls, 2*time.Second)

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Run did not return promptly after ctx cancellation")
	}
}

func TestRun_DisabledTargetIsNeverCollected(t *testing.T) {
	tgt := healthyTarget("t1", protocol.KindHost)
	tgt.Enabled = false
	st := newFakeStore(tgt)
	c := newFakeCollector(func(protocol.Target) protocol.Sample { return protocol.Sample{State: protocol.StateHealthy} })
	deps := newDeps(st, clock.Real(), map[protocol.TargetKind]collect.Collector{protocol.KindHost: c})
	deps.Sleep = fastSleep
	sched := scheduler.New(deps)

	cancel, done := runAndCancel(t, sched)
	assertNoCall(t, c.calls, 100*time.Millisecond)
	cancel()
	<-done
}

func TestRun_TargetAddedBetweenRefreshesIsPickedUp(t *testing.T) {
	st := newFakeStore() // starts empty
	c := newFakeCollector(func(protocol.Target) protocol.Sample { return protocol.Sample{State: protocol.StateHealthy} })
	deps := newDeps(st, clock.Real(), map[protocol.TargetKind]collect.Collector{protocol.KindHost: c})
	deps.Sleep = fastSleep
	sched := scheduler.New(deps)

	cancel, done := runAndCancel(t, sched)
	assertNoCall(t, c.calls, 20*time.Millisecond)

	if err := st.UpsertTarget(context.Background(), healthyTarget("late", protocol.KindHost)); err != nil {
		t.Fatalf("UpsertTarget: %v", err)
	}

	got := waitForCall(t, c.calls, 2*time.Second)
	if got.ID != "late" {
		t.Fatalf("collected target %q, want %q", got.ID, "late")
	}
	cancel()
	<-done
}

func TestRun_RemovedTargetIsForgottenByMachine(t *testing.T) {
	tgt := healthyTarget("t1", protocol.KindHost)
	st := newFakeStore(tgt)
	c := newFakeCollector(func(protocol.Target) protocol.Sample { return protocol.Sample{State: protocol.StateDown} })
	machine := incident.NewMachine(clock.Real())
	deps := scheduler.Deps{
		Store:      st,
		Clock:      clock.Real(),
		Collectors: map[protocol.TargetKind]collect.Collector{protocol.KindHost: c},
		Machine:    machine,
		Thresholds: incident.DefaultThresholds(),
		Sleep:      fastSleep,
	}
	sched := scheduler.New(deps)

	cancel, done := runAndCancel(t, sched)
	// Two confirmations against a down collector: wait for the machine to
	// actually confirm the down state before removing the target.
	waitForCall(t, c.calls, 2*time.Second)
	waitForCall(t, c.calls, 2*time.Second)
	deadline := time.Now().Add(2 * time.Second)
	for machine.Current("t1") != protocol.StateDown && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if machine.Current("t1") != protocol.StateDown {
		t.Fatalf("machine never confirmed the down state")
	}

	if err := st.DeleteTarget(context.Background(), "t1"); err != nil {
		t.Fatalf("DeleteTarget: %v", err)
	}

	deadline = time.Now().Add(2 * time.Second)
	for machine.Current("t1") != protocol.StateUnknown && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := machine.Current("t1"); got != protocol.StateUnknown {
		t.Fatalf("machine.Current(t1) = %v after removal, want unknown (Forget not called)", got)
	}

	cancel()
	<-done
}

func newFakeClock() *clock.FakeClock {
	return clock.Fake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
}
