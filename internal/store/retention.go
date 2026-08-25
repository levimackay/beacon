package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// aggregateRow is one (target_id, metric, window) group computed from the
// finer-grained tier being rolled up.
type aggregateRow struct {
	targetID string
	metric   string
	win      int64
	value    float64
	latency  float64
	rank     int64
}

// windowRankCase maps protocol.State's severity order into SQL so the worst
// state in a window can be computed by MAX() without a per-row round trip.
// Kept in lockstep with stateRank/rankState in store.go.
const windowRankCase = `CASE state WHEN 'down' THEN 3 WHEN 'unknown' THEN 2 WHEN 'warning' THEN 1 ELSE 0 END`

// Rollup aggregates samples one tier coarser (raw -> 5m -> 1h) by arithmetic
// mean, carrying the worst state observed in each window, then deletes the
// rows it just superseded. It also prunes 1h rows past their retention.
// Because a window's source rows are deleted as soon as they are folded into
// their aggregate, a second call with no new data in between is a no-op:
// there is nothing left to aggregate or prune.
//
// Only windows lying wholly on the far side of the retention cutoff are
// folded. Folding a window that the cutoff bisects would aggregate the older
// half now and the newer half on the next call, leaving two aggregate rows
// for one window: a visibly doubled point on any graph, and an unweighted
// mean when the next tier folds them. The cost of waiting is that at most
// one window's worth of data outlives its nominal retention, which is the
// cheaper error.
func (s *sqlStore) Rollup(ctx context.Context, now time.Time) (RollupStats, error) {
	var stats RollupStats

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return stats, fmt.Errorf("store: rollup: %w", err)
	}
	defer tx.Rollback()

	n, err := rollTier(ctx, tx, bucketRaw, bucket5m, bucket5mSize, now.Add(-retentionRaw).Unix())
	if err != nil {
		return stats, fmt.Errorf("store: rollup raw->5m: %w", err)
	}
	stats.Rolled5m = n

	n, err = rollTier(ctx, tx, bucket5m, bucket1h, bucket1hSize, now.Add(-retention5m).Unix())
	if err != nil {
		return stats, fmt.Errorf("store: rollup 5m->1h: %w", err)
	}
	stats.Rolled1h = n

	res, err := tx.ExecContext(ctx, `DELETE FROM samples WHERE bucket = ? AND at < ?`,
		bucket1h, now.Add(-retention1h).Unix())
	if err != nil {
		return stats, fmt.Errorf("store: rollup prune 1h: %w", err)
	}
	pruned, err := res.RowsAffected()
	if err != nil {
		return stats, fmt.Errorf("store: rollup prune 1h: %w", err)
	}
	stats.Pruned = pruned

	if err := tx.Commit(); err != nil {
		return stats, fmt.Errorf("store: rollup: %w", err)
	}
	return stats, nil
}

const (
	bucket5mSize = 300
	bucket1hSize = 3600
)

// rollTier aggregates every row in fromBucket belonging to a window that
// ends at or before cutoff into toBucket, one row per
// (target_id, metric, window-of-windowSize), then deletes the rows it just
// aggregated. It returns the number of aggregate rows written.
func rollTier(ctx context.Context, tx *sql.Tx, fromBucket, toBucket, windowSize int, cutoff int64) (int64, error) {
	// Truncate the cutoff down to a window boundary so that no partially
	// elapsed window is ever folded. See the note on Rollup.
	cutoff = (cutoff / int64(windowSize)) * int64(windowSize)

	q := fmt.Sprintf(`
		SELECT target_id, metric, (at / %d) * %d AS win,
		       AVG(value), AVG(latency_ms), MAX(%s)
		FROM samples
		WHERE bucket = ? AND at < ?
		GROUP BY target_id, metric, win
	`, windowSize, windowSize, windowRankCase)

	rows, err := tx.QueryContext(ctx, q, fromBucket, cutoff)
	if err != nil {
		return 0, fmt.Errorf("aggregate: %w", err)
	}
	var groups []aggregateRow
	for rows.Next() {
		var g aggregateRow
		if err := rows.Scan(&g.targetID, &g.metric, &g.win, &g.value, &g.latency, &g.rank); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan aggregate: %w", err)
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("aggregate: %w", err)
	}
	rows.Close()

	if len(groups) == 0 {
		return 0, nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO samples (target_id, at, bucket, state, latency_ms, metric, value, error, cert_expiry)
		VALUES (?, ?, ?, ?, ?, ?, ?, '', NULL)
	`)
	if err != nil {
		return 0, fmt.Errorf("prepare aggregate insert: %w", err)
	}
	defer stmt.Close()

	for _, g := range groups {
		if _, err := stmt.ExecContext(ctx, g.targetID, g.win, toBucket, string(rankState(g.rank)), g.latency, g.metric, g.value); err != nil {
			return 0, fmt.Errorf("insert aggregate: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM samples WHERE bucket = ? AND at < ?`, fromBucket, cutoff); err != nil {
		return 0, fmt.Errorf("delete superseded rows: %w", err)
	}

	return int64(len(groups)), nil
}

func (s *sqlStore) Stats(ctx context.Context) (Stats, error) {
	var stats Stats
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM targets`).Scan(&stats.Targets)
	if err != nil {
		return stats, fmt.Errorf("store: stats: %w", err)
	}
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM samples WHERE bucket = ?`, bucketRaw).Scan(&stats.RawSamples)
	if err != nil {
		return stats, fmt.Errorf("store: stats: %w", err)
	}
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM samples WHERE bucket = ?`, bucket5m).Scan(&stats.Bucket5m)
	if err != nil {
		return stats, fmt.Errorf("store: stats: %w", err)
	}
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM samples WHERE bucket = ?`, bucket1h).Scan(&stats.Bucket1h)
	if err != nil {
		return stats, fmt.Errorf("store: stats: %w", err)
	}
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM incidents WHERE resolved_at IS NULL`).Scan(&stats.OpenIncidents)
	if err != nil {
		return stats, fmt.Errorf("store: stats: %w", err)
	}
	var pageCount, pageSize int64
	if err := s.db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pageCount); err != nil {
		return stats, fmt.Errorf("store: stats: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		return stats, fmt.Errorf("store: stats: %w", err)
	}
	stats.SizeBytes = pageCount * pageSize
	return stats, nil
}
