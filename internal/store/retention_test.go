package store

import (
	"context"
	"testing"
	"time"

	"github.com/levimackay/beacon/internal/clock"
	"github.com/levimackay/beacon/internal/protocol"
)

// TestRetentionRollup inserts 30 days of samples at 15s spacing for one
// target and one metric, using value = sample index as a known pattern so
// window means can be checked against a closed-form expectation without
// needing to re-read raw rows that Rollup has since deleted.
func TestRetentionRollup(t *testing.T) {
	ctx := context.Background()

	const (
		targetID = "host-1"
		metric   = protocol.MetricCPUPercent
		interval = 15 * time.Second
		days     = 30
	)
	n := int(days * 24 * time.Hour / interval) // 172,800

	// Align start to a 5-minute (and hence hour) boundary so every rollup
	// window this test cares about is either wholly before or wholly after
	// each retention cutoff, with no partially-aggregated window to reason
	// about.
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	start = start.Add(-time.Duration(start.Unix()%300) * time.Second)
	now := start.Add(days * 24 * time.Hour)

	c := clock.Fake(now)
	s, _ := openTestStore(t, c)
	st := s.(*sqlStore)

	if err := s.UpsertTarget(ctx, protocol.Target{ID: targetID, Kind: protocol.KindHost, Name: "h", IntervalSeconds: 15, Enabled: true}); err != nil {
		t.Fatalf("UpsertTarget: %v", err)
	}

	// Insert all 172,800 rows in a single transaction; one InsertSample call
	// per row (each opening its own transaction) would be far too slow.
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	stmt, err := tx.PrepareContext(ctx, insertSampleSQL)
	if err != nil {
		t.Fatalf("PrepareContext: %v", err)
	}
	for i := 0; i < n; i++ {
		at := start.Add(time.Duration(i) * interval).Unix()
		if _, err := stmt.ExecContext(ctx, targetID, at, string(protocol.StateHealthy), 0.0, metric, float64(i), "", nil); err != nil {
			stmt.Close()
			t.Fatalf("insert sample %d: %v", i, err)
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	statsBefore, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats before rollup: %v", err)
	}
	if statsBefore.RawSamples != int64(n) {
		t.Fatalf("RawSamples before rollup = %d, want %d", statsBefore.RawSamples, n)
	}

	rs, err := s.Rollup(ctx, now)
	if err != nil {
		t.Fatalf("Rollup: %v", err)
	}

	const (
		rawCutoff  = 6 * time.Hour
		fiveCutoff = 7 * 24 * time.Hour
		hourCutoff = 90 * 24 * time.Hour
	)
	cutoffRaw := now.Add(-rawCutoff)
	cutoff5m := now.Add(-fiveCutoff)
	cutoff1h := now.Add(-hourCutoff)

	// (a) no raw rows older than 6h remain.
	oldRaw, err := st.countOlder(ctx, bucketRaw, cutoffRaw)
	if err != nil {
		t.Fatalf("countOlder raw: %v", err)
	}
	if oldRaw != 0 {
		t.Fatalf("raw rows older than 6h remaining = %d, want 0", oldRaw)
	}

	// (b) no 5m rows older than 7d remain.
	old5m, err := st.countOlder(ctx, bucket5m, cutoff5m)
	if err != nil {
		t.Fatalf("countOlder 5m: %v", err)
	}
	if old5m != 0 {
		t.Fatalf("5m rows older than 7d remaining = %d, want 0", old5m)
	}

	// (c) no 1h rows older than 90d remain (nothing should have been pruned
	// at all, since all data is younger than 90d).
	old1h, err := st.countOlder(ctx, bucket1h, cutoff1h)
	if err != nil {
		t.Fatalf("countOlder 1h: %v", err)
	}
	if old1h != 0 {
		t.Fatalf("1h rows older than 90d remaining = %d, want 0", old1h)
	}
	if rs.Pruned != 0 {
		t.Fatalf("Rollup pruned %d rows, want 0 (nothing is older than 90d)", rs.Pruned)
	}

	// Expected row counts, computed from the aligned boundaries.
	wantRaw := int64(rawCutoff / interval)                               // 1440
	wantBucket5m := int64((cutoffRaw.Sub(cutoff5m)) / (5 * time.Minute)) // 1944
	wantBucket1h := int64((cutoff5m.Sub(start)) / time.Hour)             // 552

	statsAfter, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats after rollup: %v", err)
	}
	if statsAfter.RawSamples != wantRaw {
		t.Fatalf("RawSamples after rollup = %d, want %d", statsAfter.RawSamples, wantRaw)
	}
	if statsAfter.Bucket5m != wantBucket5m {
		t.Fatalf("Bucket5m after rollup = %d, want %d", statsAfter.Bucket5m, wantBucket5m)
	}
	if statsAfter.Bucket1h != wantBucket1h {
		t.Fatalf("Bucket1h after rollup = %d, want %d", statsAfter.Bucket1h, wantBucket1h)
	}

	// (d) the mean stored in a 5m bucket equals the mean of the raw rows it
	// replaced, within 1e-6. Pick a window comfortably inside the surviving
	// 5m range: 1 day after cutoff5m.
	winTime := cutoff5m.Add(24 * time.Hour)
	w := int64(winTime.Sub(start) / (5 * time.Minute)) // window index, 20 raw samples each
	wantMean := float64(w)*20 + 9.5

	points, err := s.SampleSeries(ctx, targetID, metric, winTime)
	if err != nil {
		t.Fatalf("SampleSeries: %v", err)
	}
	if len(points) == 0 {
		t.Fatalf("SampleSeries returned no points at or after %v", winTime)
	}
	if !points[0].At.Equal(winTime) {
		t.Fatalf("SampleSeries[0].At = %v, want %v", points[0].At, winTime)
	}
	if diff := points[0].Value - wantMean; diff > 1e-6 || diff < -1e-6 {
		t.Fatalf("5m bucket mean = %v, want %v (diff %v)", points[0].Value, wantMean, diff)
	}

	// (e) running Rollup again with no new data changes nothing.
	rs2, err := s.Rollup(ctx, now)
	if err != nil {
		t.Fatalf("second Rollup: %v", err)
	}
	if rs2.Rolled5m != 0 || rs2.Rolled1h != 0 || rs2.Pruned != 0 {
		t.Fatalf("second Rollup = %+v, want all zero", rs2)
	}

	statsAfter2, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats after second rollup: %v", err)
	}
	if statsAfter2 != statsAfter {
		t.Fatalf("Stats changed after idempotent Rollup: before=%+v after=%+v", statsAfter, statsAfter2)
	}
}

// countOlder is a whitebox test helper: it counts rows in the given bucket
// older than cutoff, straight against the database, so the test does not
// need a public Store method just to assert retention held.
func (s *sqlStore) countOlder(ctx context.Context, bucket int, cutoff time.Time) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM samples WHERE bucket = ? AND at < ?`, bucket, cutoff.Unix()).Scan(&n)
	return n, err
}
