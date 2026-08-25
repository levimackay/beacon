package api

import (
	"context"
	"sync"
	"time"

	"github.com/levimackay/beacon/internal/protocol"
	"github.com/levimackay/beacon/internal/store"
)

// fakeStore is a minimal in-memory store.Store used only by this package's
// tests, so API tests stay fast and don't drag in the real SQLite
// implementation.
type fakeStore struct {
	mu        sync.Mutex
	targets   map[string]protocol.Target
	samples   map[string]protocol.Sample // latest sample per target id
	incidents []protocol.Incident
	nextID    int64
	audit     []store.AuditRow

	upsertCalls int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		targets: make(map[string]protocol.Target),
		samples: make(map[string]protocol.Sample),
	}
}

func (f *fakeStore) UpsertTarget(_ context.Context, t protocol.Target) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upsertCalls++
	f.targets[t.ID] = t
	return nil
}

func (f *fakeStore) DeleteTarget(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.targets, id)
	return nil
}

func (f *fakeStore) Targets(_ context.Context) ([]protocol.Target, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]protocol.Target, 0, len(f.targets))
	for _, t := range f.targets {
		out = append(out, t)
	}
	return out, nil
}

func (f *fakeStore) InsertSample(_ context.Context, s protocol.Sample) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.samples[s.TargetID] = s
	return nil
}

func (f *fakeStore) LatestSamples(_ context.Context) (map[string]protocol.Sample, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]protocol.Sample, len(f.samples))
	for k, v := range f.samples {
		out[k] = v
	}
	return out, nil
}

func (f *fakeStore) SampleSeries(_ context.Context, _, _ string, _ time.Time) ([]store.Point, error) {
	return nil, nil
}

func (f *fakeStore) OpenIncident(_ context.Context, in protocol.Incident) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	in.ID = f.nextID
	f.incidents = append(f.incidents, in)
	return in.ID, nil
}

func (f *fakeStore) ResolveIncident(_ context.Context, targetID string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.incidents {
		if f.incidents[i].TargetID == targetID && f.incidents[i].ResolvedAt == nil {
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
		if in.ResolvedAt == nil {
			out = append(out, in)
		}
	}
	return out, nil
}

func (f *fakeStore) Incidents(_ context.Context, filter store.IncidentFilter) ([]protocol.Incident, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []protocol.Incident
	for _, in := range f.incidents {
		if filter.TargetID != "" && in.TargetID != filter.TargetID {
			continue
		}
		if !filter.Since.IsZero() && in.StartedAt.Before(filter.Since) {
			continue
		}
		if !filter.Until.IsZero() && in.StartedAt.After(filter.Until) {
			continue
		}
		out = append(out, in)
		if filter.Limit > 0 && len(out) >= filter.Limit {
			break
		}
	}
	return out, nil
}

func (f *fakeStore) Audit(_ context.Context, principal, action, target, result string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.audit = append(f.audit, store.AuditRow{
		At: time.Now(), Principal: principal, Action: action, Target: target, Result: result,
	})
	return nil
}

func (f *fakeStore) AuditTail(_ context.Context, limit int) ([]store.AuditRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if limit <= 0 || limit > len(f.audit) {
		limit = len(f.audit)
	}
	start := len(f.audit) - limit
	out := make([]store.AuditRow, limit)
	copy(out, f.audit[start:])
	return out, nil
}

func (f *fakeStore) Rollup(_ context.Context, _ time.Time) (store.RollupStats, error) {
	return store.RollupStats{}, nil
}

func (f *fakeStore) Stats(_ context.Context) (store.Stats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var openCount int64
	for _, in := range f.incidents {
		if in.ResolvedAt == nil {
			openCount++
		}
	}
	return store.Stats{
		Targets:       int64(len(f.targets)),
		OpenIncidents: openCount,
	}, nil
}

func (f *fakeStore) Close() error { return nil }

func (f *fakeStore) auditLen() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.audit)
}

func (f *fakeStore) upserts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.upsertCalls
}
