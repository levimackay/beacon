package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/levimackay/beacon/internal/clock"
	"github.com/levimackay/beacon/internal/protocol"
)

// countBucketRows reports how many rows exist in a tier, and how many
// distinct windows they cover. Those two numbers must be equal: a window
// aggregated twice is a doubled point on a graph.
func countBucketRows(t *testing.T, s Store, bucket int) (rows, windows int64) {
	t.Helper()
	db := s.(*sqlStore).db
	if err := db.QueryRow(`SELECT COUNT(*) FROM samples WHERE bucket = ?`, bucket).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(DISTINCT at) FROM samples WHERE bucket = ?`, bucket).Scan(&windows); err != nil {
		t.Fatal(err)
	}
	return rows, windows
}

// A live stream is rolled up repeatedly while samples keep arriving, so the
// retention cutoff lands at an arbitrary point rather than neatly on a
// window boundary. Every window must still produce exactly one aggregate.
func TestRollupNeverSplitsAWindowAcrossCalls(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	c := clock.Fake(start)

	s, err := Open(filepath.Join(t.TempDir(), "beacon.db"), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.UpsertTarget(ctx, protocol.Target{
		ID: "host-1", Kind: protocol.KindHost, Name: "This Mac", IntervalSeconds: 15, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	// Eight hours of samples at 15s, so a chunk is already past the 6h
	// raw retention.
	const step = 15 * time.Second
	for at := start; at.Before(start.Add(8 * time.Hour)); at = at.Add(step) {
		if err := s.InsertSample(ctx, protocol.Sample{
			TargetID: "host-1",
			At:       at,
			State:    protocol.StateHealthy,
			Metrics:  map[string]float64{protocol.MetricCPUPercent: 50},
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Roll up at times deliberately offset from any 5-minute boundary.
	for _, offset := range []time.Duration{
		8*time.Hour + 137*time.Second,
		8*time.Hour + 421*time.Second,
		8*time.Hour + 923*time.Second,
	} {
		if _, err := s.Rollup(ctx, start.Add(offset)); err != nil {
			t.Fatalf("Rollup at +%v: %v", offset, err)
		}
	}

	rows, windows := countBucketRows(t, s, bucket5m)
	if rows == 0 {
		t.Fatal("nothing was rolled up; the test is not exercising the path")
	}
	if rows != windows {
		t.Fatalf("%d aggregate rows across only %d distinct windows: a window was folded twice", rows, windows)
	}

	// Every surviving aggregate must sit on a window boundary.
	db := s.(*sqlStore).db
	var misaligned int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM samples WHERE bucket = ? AND at % ? != 0`, bucket5m, bucket5mSize).Scan(&misaligned); err != nil {
		t.Fatal(err)
	}
	if misaligned != 0 {
		t.Fatalf("%d aggregate rows are not aligned to a %ds window", misaligned, bucket5mSize)
	}
}

// The mean stored for a window must reflect every raw sample in it. If a
// window were folded in two halves, the value would be wrong as well as
// duplicated.
func TestRollupMeanCoversTheWholeWindow(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	c := clock.Fake(start)

	s, err := Open(filepath.Join(t.TempDir(), "beacon.db"), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.UpsertTarget(ctx, protocol.Target{
		ID: "host-1", Kind: protocol.KindHost, Name: "This Mac", IntervalSeconds: 15, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	// One 5-minute window, twenty samples ramping 1..20, mean 10.5.
	winStart := start
	for i := range 20 {
		if err := s.InsertSample(ctx, protocol.Sample{
			TargetID: "host-1",
			At:       winStart.Add(time.Duration(i) * 15 * time.Second),
			State:    protocol.StateHealthy,
			Metrics:  map[string]float64{protocol.MetricCPUPercent: float64(i + 1)},
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Cut off mid-window on the first pass, well past it on the second.
	if _, err := s.Rollup(ctx, winStart.Add(6*time.Hour+150*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rollup(ctx, winStart.Add(7*time.Hour)); err != nil {
		t.Fatal(err)
	}

	pts, err := s.SampleSeries(ctx, "host-1", protocol.MetricCPUPercent, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 1 {
		t.Fatalf("expected exactly one aggregate point, got %d: %+v", len(pts), pts)
	}
	if got := pts[0].Value; got < 10.4999 || got > 10.5001 {
		t.Fatalf("window mean = %v, want 10.5 (the mean of all 20 samples)", got)
	}
}
