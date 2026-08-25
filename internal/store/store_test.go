package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/levimackay/beacon/internal/clock"
	"github.com/levimackay/beacon/internal/protocol"
)

func openTestStore(t *testing.T, c clock.Clock) (Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "beacon.db")
	s, err := Open(path, c)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestOpenIsIdempotentAndPersists(t *testing.T) {
	ctx := context.Background()
	c := clock.Fake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	path := filepath.Join(t.TempDir(), "beacon.db")

	s1, err := Open(path, c)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	target := protocol.Target{ID: "t1", Kind: protocol.KindHost, Name: "host", IntervalSeconds: 30, Enabled: true}
	if err := s1.UpsertTarget(ctx, target); err != nil {
		t.Fatalf("UpsertTarget: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopening the same file must not fail, and must not wipe rows, since
	// schema application uses CREATE TABLE IF NOT EXISTS.
	s2, err := Open(path, c)
	if err != nil {
		t.Fatalf("reopen Open: %v", err)
	}
	defer s2.Close()

	got, err := s2.Targets(ctx)
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(got) != 1 || got[0].ID != "t1" {
		t.Fatalf("Targets after reopen = %+v, want [t1]", got)
	}
}

func TestUpsertTargetThenTargets(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestStore(t, clock.Fake(time.Now()))

	target := protocol.Target{
		ID: "web1", Kind: protocol.KindWebsite, Name: "Example",
		Address: "https://example.com", IntervalSeconds: 60, ExpectStatus: 200, Enabled: true,
	}
	if err := s.UpsertTarget(ctx, target); err != nil {
		t.Fatalf("UpsertTarget: %v", err)
	}

	got, err := s.Targets(ctx)
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Targets len = %d, want 1", len(got))
	}
	if got[0] != target {
		t.Fatalf("Targets[0] = %+v, want %+v", got[0], target)
	}

	// A second upsert of the same id updates rather than duplicates.
	target.Name = "Example Updated"
	target.IntervalSeconds = 120
	if err := s.UpsertTarget(ctx, target); err != nil {
		t.Fatalf("second UpsertTarget: %v", err)
	}
	got, err = s.Targets(ctx)
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Targets len after update = %d, want 1", len(got))
	}
	if got[0].Name != "Example Updated" || got[0].IntervalSeconds != 120 {
		t.Fatalf("Targets[0] after update = %+v, want updated fields", got[0])
	}

	if err := s.DeleteTarget(ctx, "web1"); err != nil {
		t.Fatalf("DeleteTarget: %v", err)
	}
	got, err = s.Targets(ctx)
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Targets after delete = %+v, want empty", got)
	}
}

func TestInsertSampleAndLatestSamples(t *testing.T) {
	ctx := context.Background()
	c := clock.Fake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	s, _ := openTestStore(t, c)

	older := protocol.Sample{
		TargetID: "host-1", At: c.Now(), State: protocol.StateHealthy, LatencyMS: 1,
		Metrics: map[string]float64{protocol.MetricCPUPercent: 10, protocol.MetricMemPercent: 20},
	}
	if err := s.InsertSample(ctx, older); err != nil {
		t.Fatalf("InsertSample (older): %v", err)
	}

	c.Advance(time.Minute)
	newer := protocol.Sample{
		TargetID: "host-1", At: c.Now(), State: protocol.StateWarning, LatencyMS: 5,
		Metrics: map[string]float64{protocol.MetricCPUPercent: 90, protocol.MetricMemPercent: 91},
		Error:   "high load",
	}
	if err := s.InsertSample(ctx, newer); err != nil {
		t.Fatalf("InsertSample (newer): %v", err)
	}

	// A second target with no metrics must still be persisted and retrievable.
	noMetrics := protocol.Sample{TargetID: "web-1", At: c.Now(), State: protocol.StateDown, Error: "connection refused"}
	if err := s.InsertSample(ctx, noMetrics); err != nil {
		t.Fatalf("InsertSample (no metrics): %v", err)
	}

	latest, err := s.LatestSamples(ctx)
	if err != nil {
		t.Fatalf("LatestSamples: %v", err)
	}
	if len(latest) != 2 {
		t.Fatalf("LatestSamples len = %d, want 2", len(latest))
	}

	got, ok := latest["host-1"]
	if !ok {
		t.Fatalf("LatestSamples missing host-1")
	}
	if got.State != protocol.StateWarning || got.Error != "high load" {
		t.Fatalf("LatestSamples[host-1] = %+v, want the newer sample", got)
	}
	if got.Metrics[protocol.MetricCPUPercent] != 90 || got.Metrics[protocol.MetricMemPercent] != 91 {
		t.Fatalf("LatestSamples[host-1].Metrics = %+v, want cpu=90 mem=91", got.Metrics)
	}
	if !got.At.Equal(newer.At) {
		t.Fatalf("LatestSamples[host-1].At = %v, want %v", got.At, newer.At)
	}

	gotWeb, ok := latest["web-1"]
	if !ok {
		t.Fatalf("LatestSamples missing web-1")
	}
	if gotWeb.State != protocol.StateDown || gotWeb.Error != "connection refused" {
		t.Fatalf("LatestSamples[web-1] = %+v, want state=down error=connection refused", gotWeb)
	}
	if len(gotWeb.Metrics) != 0 {
		t.Fatalf("LatestSamples[web-1].Metrics = %+v, want empty", gotWeb.Metrics)
	}
}

func TestSampleSeriesOrderAndSince(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := clock.Fake(start)
	s, _ := openTestStore(t, c)

	for i := 0; i < 5; i++ {
		sample := protocol.Sample{
			TargetID: "host-1", At: c.Now(), State: protocol.StateHealthy,
			Metrics: map[string]float64{protocol.MetricCPUPercent: float64(i)},
		}
		if err := s.InsertSample(ctx, sample); err != nil {
			t.Fatalf("InsertSample %d: %v", i, err)
		}
		c.Advance(time.Minute)
	}

	// since = start+2min should return points 2,3,4 in ascending order.
	since := start.Add(2 * time.Minute)
	points, err := s.SampleSeries(ctx, "host-1", protocol.MetricCPUPercent, since)
	if err != nil {
		t.Fatalf("SampleSeries: %v", err)
	}
	if len(points) != 3 {
		t.Fatalf("SampleSeries len = %d, want 3: %+v", len(points), points)
	}
	for i, p := range points {
		wantValue := float64(i + 2)
		if p.Value != wantValue {
			t.Fatalf("SampleSeries[%d].Value = %v, want %v", i, p.Value, wantValue)
		}
		if i > 0 && points[i-1].At.After(p.At) {
			t.Fatalf("SampleSeries not ascending at index %d: %v then %v", i, points[i-1].At, p.At)
		}
	}
}

func TestIncidentLifecycle(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := clock.Fake(start)
	s, _ := openTestStore(t, c)

	// Resolving with nothing open is a nil no-op.
	if err := s.ResolveIncident(ctx, "host-1", c.Now()); err != nil {
		t.Fatalf("ResolveIncident on none open: %v", err)
	}

	in := protocol.Incident{TargetID: "host-1", TargetName: "Host One", State: protocol.StateDown, StartedAt: c.Now(), Summary: "cpu 96%"}
	id1, err := s.OpenIncident(ctx, in)
	if err != nil {
		t.Fatalf("OpenIncident: %v", err)
	}
	if id1 == 0 {
		t.Fatalf("OpenIncident returned id 0")
	}

	// Opening again for the same target must not create a second incident.
	id2, err := s.OpenIncident(ctx, in)
	if err != nil {
		t.Fatalf("second OpenIncident: %v", err)
	}
	if id2 != id1 {
		t.Fatalf("second OpenIncident id = %d, want %d (same incident)", id2, id1)
	}

	open, err := s.OpenIncidents(ctx)
	if err != nil {
		t.Fatalf("OpenIncidents: %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("OpenIncidents len = %d, want 1", len(open))
	}

	c.Advance(10 * time.Minute)
	resolvedAt := c.Now()
	if err := s.ResolveIncident(ctx, "host-1", resolvedAt); err != nil {
		t.Fatalf("ResolveIncident: %v", err)
	}

	open, err = s.OpenIncidents(ctx)
	if err != nil {
		t.Fatalf("OpenIncidents after resolve: %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("OpenIncidents after resolve = %+v, want empty", open)
	}

	all, err := s.Incidents(ctx, IncidentFilter{})
	if err != nil {
		t.Fatalf("Incidents: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("Incidents len = %d, want 1", len(all))
	}
	if all[0].ResolvedAt == nil || !all[0].ResolvedAt.Equal(resolvedAt) {
		t.Fatalf("Incidents[0].ResolvedAt = %v, want %v", all[0].ResolvedAt, resolvedAt)
	}

	// Resolving again, now that there's nothing open, stays a no-op.
	if err := s.ResolveIncident(ctx, "host-1", c.Now()); err != nil {
		t.Fatalf("ResolveIncident again: %v", err)
	}
}

func TestIncidentsFilter(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := clock.Fake(start)
	s, _ := openTestStore(t, c)

	for i, target := range []string{"a", "b", "a"} {
		in := protocol.Incident{TargetID: target, TargetName: target, State: protocol.StateDown, StartedAt: c.Now(), Summary: "x"}
		if _, err := s.OpenIncident(ctx, in); err != nil {
			t.Fatalf("OpenIncident %d: %v", i, err)
		}
		if err := s.ResolveIncident(ctx, target, c.Now()); err != nil {
			t.Fatalf("ResolveIncident %d: %v", i, err)
		}
		c.Advance(time.Hour)
	}

	got, err := s.Incidents(ctx, IncidentFilter{TargetID: "a"})
	if err != nil {
		t.Fatalf("Incidents filtered: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Incidents(TargetID=a) len = %d, want 2", len(got))
	}
	for _, in := range got {
		if in.TargetID != "a" {
			t.Fatalf("Incidents(TargetID=a) returned %+v", in)
		}
	}

	got, err = s.Incidents(ctx, IncidentFilter{Limit: 1})
	if err != nil {
		t.Fatalf("Incidents limited: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Incidents(Limit=1) len = %d, want 1", len(got))
	}
}

func TestAuditTailInsertionOrder(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := clock.Fake(start)
	s, _ := openTestStore(t, c)

	actions := []string{"create", "update", "delete"}
	for _, a := range actions {
		if err := s.Audit(ctx, "cli", a, "target-1", "ok"); err != nil {
			t.Fatalf("Audit(%s): %v", a, err)
		}
		c.Advance(time.Second)
	}

	rows, err := s.AuditTail(ctx, 10)
	if err != nil {
		t.Fatalf("AuditTail: %v", err)
	}
	if len(rows) != len(actions) {
		t.Fatalf("AuditTail len = %d, want %d", len(rows), len(actions))
	}
	for i, a := range actions {
		if rows[i].Action != a {
			t.Fatalf("AuditTail[%d].Action = %q, want %q (order: %+v)", i, rows[i].Action, a, rows)
		}
	}

	// A smaller limit still returns the tail in insertion order.
	rows, err = s.AuditTail(ctx, 2)
	if err != nil {
		t.Fatalf("AuditTail(2): %v", err)
	}
	if len(rows) != 2 || rows[0].Action != "update" || rows[1].Action != "delete" {
		t.Fatalf("AuditTail(2) = %+v, want [update delete]", rows)
	}
}

func TestStats(t *testing.T) {
	ctx := context.Background()
	c := clock.Fake(time.Now())
	s, _ := openTestStore(t, c)

	if err := s.UpsertTarget(ctx, protocol.Target{ID: "t1", Kind: protocol.KindHost, Name: "h", IntervalSeconds: 30, Enabled: true}); err != nil {
		t.Fatalf("UpsertTarget: %v", err)
	}
	if err := s.InsertSample(ctx, protocol.Sample{TargetID: "t1", At: c.Now(), State: protocol.StateHealthy, Metrics: map[string]float64{"cpu_percent": 1}}); err != nil {
		t.Fatalf("InsertSample: %v", err)
	}
	if _, err := s.OpenIncident(ctx, protocol.Incident{TargetID: "t1", TargetName: "h", State: protocol.StateDown, StartedAt: c.Now()}); err != nil {
		t.Fatalf("OpenIncident: %v", err)
	}

	stats, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Targets != 1 || stats.RawSamples != 1 || stats.OpenIncidents != 1 {
		t.Fatalf("Stats = %+v, want Targets=1 RawSamples=1 OpenIncidents=1", stats)
	}
	if stats.SizeBytes <= 0 {
		t.Fatalf("Stats.SizeBytes = %d, want > 0", stats.SizeBytes)
	}
}
