package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/levimackay/beacon/internal/protocol"
)

const insertSampleSQL = `
	INSERT INTO samples (target_id, at, bucket, state, latency_ms, metric, value, error, cert_expiry)
	VALUES (?, ?, 0, ?, ?, ?, ?, ?, ?)
`

// InsertSample stores one observation. It writes one row per metric; a
// sample with no metrics still writes one row, under the sentinel metric
// name, so its state/latency/error are not lost.
func (s *sqlStore) InsertSample(ctx context.Context, sample protocol.Sample) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: insert sample: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, insertSampleSQL)
	if err != nil {
		return fmt.Errorf("store: insert sample: %w", err)
	}
	defer stmt.Close()

	at := sample.At.Unix()
	var certExpiry sql.NullInt64
	if sample.CertExpiry != nil {
		certExpiry = sql.NullInt64{Int64: sample.CertExpiry.Unix(), Valid: true}
	}

	insert := func(metric string, value float64) error {
		_, err := stmt.ExecContext(ctx, sample.TargetID, at, string(sample.State), sample.LatencyMS, metric, value, sample.Error, certExpiry)
		return err
	}

	if len(sample.Metrics) == 0 {
		if err := insert(sentinelMetric, 0); err != nil {
			return fmt.Errorf("store: insert sample: %w", err)
		}
	} else {
		for metric, value := range sample.Metrics {
			if err := insert(metric, value); err != nil {
				return fmt.Errorf("store: insert sample: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: insert sample: %w", err)
	}
	return nil
}

// LatestSamples returns, per target, the most recent raw sample with its
// metrics reassembled into one map.
func (s *sqlStore) LatestSamples(ctx context.Context) (map[string]protocol.Sample, error) {
	const q = `
		SELECT s.target_id, s.at, s.state, s.latency_ms, s.metric, s.value, s.error, s.cert_expiry
		FROM samples s
		JOIN (
			SELECT target_id, MAX(at) AS at
			FROM samples
			WHERE bucket = 0
			GROUP BY target_id
		) latest ON latest.target_id = s.target_id AND latest.at = s.at
		WHERE s.bucket = 0
		ORDER BY s.target_id, s.metric
	`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store: latest samples: %w", err)
	}
	defer rows.Close()

	out := make(map[string]protocol.Sample)
	for rows.Next() {
		var targetID, state, metric, errStr string
		var latencyMS, value float64
		var certExpiry sql.NullInt64
		var atUnix int64
		if err := rows.Scan(&targetID, &atUnix, &state, &latencyMS, &metric, &value, &errStr, &certExpiry); err != nil {
			return nil, fmt.Errorf("store: scan latest sample: %w", err)
		}

		sample, ok := out[targetID]
		if !ok {
			sample = protocol.Sample{
				TargetID:  targetID,
				At:        time.Unix(atUnix, 0).UTC(),
				State:     protocol.State(state),
				LatencyMS: latencyMS,
				Error:     errStr,
			}
			if certExpiry.Valid {
				t := time.Unix(certExpiry.Int64, 0).UTC()
				sample.CertExpiry = &t
			}
		}
		if metric != sentinelMetric {
			if sample.Metrics == nil {
				sample.Metrics = make(map[string]float64)
			}
			sample.Metrics[metric] = value
		}
		out[targetID] = sample
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: latest samples: %w", err)
	}
	return out, nil
}

// SampleSeries returns points for one target and metric, across every
// retention tier (raw, 5m, 1h), ascending by time, at or after since.
func (s *sqlStore) SampleSeries(ctx context.Context, targetID, metric string, since time.Time) ([]Point, error) {
	const q = `
		SELECT at, value FROM samples
		WHERE target_id = ? AND metric = ? AND at >= ?
		ORDER BY at ASC
	`
	rows, err := s.db.QueryContext(ctx, q, targetID, metric, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("store: sample series: %w", err)
	}
	defer rows.Close()

	var out []Point
	for rows.Next() {
		var atUnix int64
		var value float64
		if err := rows.Scan(&atUnix, &value); err != nil {
			return nil, fmt.Errorf("store: scan sample series: %w", err)
		}
		out = append(out, Point{At: time.Unix(atUnix, 0).UTC(), Value: value})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: sample series: %w", err)
	}
	return out, nil
}
