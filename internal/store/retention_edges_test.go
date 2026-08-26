package store

import (
	"context"
	"testing"
	"time"

	"github.com/levimackay/beacon/internal/clock"
	"github.com/levimackay/beacon/internal/protocol"
)

// Rollup only ever touches the samples table; nothing in retention.go
// queries or deletes from incidents. An incident far older than every
// sample retention cutoff (raw 6h, 5m 7d, 1h 90d) must still survive a
// rollup untouched: incident history has no retention policy of its own
// (design.md's Retention section only ever describes sample tiers), and a
// future change to Rollup could easily start pruning it by age the same
// way samples are, by accident.
func TestRollupNeverPrunesIncidents(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	c := clock.Fake(now)
	s, _ := openTestStore(t, c)

	longAgo := now.Add(-200 * 24 * time.Hour) // well past the 90d 1h-bucket retention
	if err := s.UpsertTarget(ctx, protocol.Target{ID: "host-1", Kind: protocol.KindHost, Name: "h", IntervalSeconds: 15, Enabled: true}); err != nil {
		t.Fatalf("UpsertTarget: %v", err)
	}
	if _, err := s.OpenIncident(ctx, protocol.Incident{
		TargetID: "host-1", TargetName: "h", State: protocol.StateDown, StartedAt: longAgo, Summary: "old outage",
	}); err != nil {
		t.Fatalf("OpenIncident: %v", err)
	}
	if err := s.ResolveIncident(ctx, "host-1", longAgo.Add(time.Hour)); err != nil {
		t.Fatalf("ResolveIncident: %v", err)
	}

	if _, err := s.Rollup(ctx, now); err != nil {
		t.Fatalf("Rollup: %v", err)
	}

	got, err := s.Incidents(ctx, IncidentFilter{Limit: 10})
	if err != nil {
		t.Fatalf("Incidents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("incidents after rollup = %+v, want the old incident still present", got)
	}
	if got[0].Summary != "old outage" {
		t.Fatalf("incident = %+v, want the original summary preserved", got[0])
	}
}

// DeleteTarget resolves any open incident (see deletetarget_test.go) but
// must not touch the samples table: a deleted target's historical samples
// stay queryable, and age out through the normal retention/rollup path like
// any other sample, rather than being cascade-deleted. This mirrors how
// incident history for a deleted target is deliberately kept rather than
// erased.
func TestDeletingATargetDoesNotDeleteItsHistoricalSamples(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	c := clock.Fake(now)
	s, _ := openTestStore(t, c)

	tgt := protocol.Target{ID: "host-1", Kind: protocol.KindHost, Name: "h", IntervalSeconds: 15, Enabled: true}
	if err := s.UpsertTarget(ctx, tgt); err != nil {
		t.Fatalf("UpsertTarget: %v", err)
	}
	if err := s.InsertSample(ctx, protocol.Sample{
		TargetID: "host-1", At: now, State: protocol.StateHealthy,
		Metrics: map[string]float64{protocol.MetricCPUPercent: 10},
	}); err != nil {
		t.Fatalf("InsertSample: %v", err)
	}

	before, err := s.LatestSamples(ctx)
	if err != nil {
		t.Fatalf("LatestSamples before delete: %v", err)
	}
	if _, ok := before["host-1"]; !ok {
		t.Fatal("precondition: sample not recorded")
	}

	if err := s.DeleteTarget(ctx, "host-1"); err != nil {
		t.Fatalf("DeleteTarget: %v", err)
	}

	pts, err := s.SampleSeries(ctx, "host-1", protocol.MetricCPUPercent, time.Time{})
	if err != nil {
		t.Fatalf("SampleSeries after delete: %v", err)
	}
	if len(pts) != 1 {
		t.Fatalf("SampleSeries after deleting the target = %+v, want the 1 historical sample still present", pts)
	}
}

// Since/Until are boundary-inclusive at the real SQL layer too (started_at
// >= Since AND started_at <= Until): only the API package's fake-store
// tests covered this filter before, never the real store's SQL.
func TestIncidentsFilterSinceUntilBoundariesAreInclusive(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := clock.Fake(start)
	s, _ := openTestStore(t, c)

	day1 := start
	day2 := start.Add(24 * time.Hour)
	day3 := start.Add(48 * time.Hour)
	ids := []string{"t1", "t2", "t3"}
	times := []time.Time{day1, day2, day3}
	for i := range ids {
		if _, err := s.OpenIncident(ctx, protocol.Incident{TargetID: ids[i], TargetName: ids[i], State: protocol.StateDown, StartedAt: times[i]}); err != nil {
			t.Fatalf("OpenIncident %d: %v", i, err)
		}
	}

	got, err := s.Incidents(ctx, IncidentFilter{Since: day1, Until: day2})
	if err != nil {
		t.Fatalf("Incidents: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Incidents(Since=day1,Until=day2) = %+v, want exactly day1 and day2 (both boundary-inclusive)", got)
	}
	for _, in := range got {
		if in.StartedAt.After(day2) {
			t.Fatalf("incident %+v started after Until", in)
		}
		if in.StartedAt.Before(day1) {
			t.Fatalf("incident %+v started before Since", in)
		}
	}
}

// ORDER BY started_at DESC plus Limit must return the most recent
// incidents, not merely the right count of arbitrary ones.
func TestIncidentsFilterLimitReturnsMostRecentFirst(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := clock.Fake(start)
	s, _ := openTestStore(t, c)

	for i, tid := range []string{"oldest", "middle", "newest"} {
		if _, err := s.OpenIncident(ctx, protocol.Incident{TargetID: tid, TargetName: tid, State: protocol.StateDown, StartedAt: c.Now()}); err != nil {
			t.Fatalf("OpenIncident %d: %v", i, err)
		}
		c.Advance(time.Hour)
	}

	got, err := s.Incidents(ctx, IncidentFilter{Limit: 1})
	if err != nil {
		t.Fatalf("Incidents: %v", err)
	}
	if len(got) != 1 || got[0].TargetID != "newest" {
		t.Fatalf("Incidents(Limit=1) = %+v, want exactly the newest incident", got)
	}
}
