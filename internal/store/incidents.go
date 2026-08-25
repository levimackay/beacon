package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/levimackay/beacon/internal/protocol"
)

// OpenIncident opens a new incident, unless the target already has one open,
// in which case it returns the existing incident's id without writing.
func (s *sqlStore) OpenIncident(ctx context.Context, in protocol.Incident) (int64, error) {
	var existing int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM incidents WHERE target_id = ? AND resolved_at IS NULL`, in.TargetID,
	).Scan(&existing)
	switch {
	case err == nil:
		return existing, nil
	case err != sql.ErrNoRows:
		return 0, fmt.Errorf("store: open incident: %w", err)
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO incidents (target_id, target_name, state, started_at, summary) VALUES (?, ?, ?, ?, ?)`,
		in.TargetID, in.TargetName, string(in.State), in.StartedAt.Unix(), in.Summary)
	if err != nil {
		return 0, fmt.Errorf("store: open incident: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: open incident: %w", err)
	}
	return id, nil
}

// ResolveIncident closes the target's open incident, if any. A target with
// no open incident is a no-op, not an error.
func (s *sqlStore) ResolveIncident(ctx context.Context, targetID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE incidents SET resolved_at = ? WHERE target_id = ? AND resolved_at IS NULL`,
		at.Unix(), targetID)
	if err != nil {
		return fmt.Errorf("store: resolve incident: %w", err)
	}
	return nil
}

func (s *sqlStore) OpenIncidents(ctx context.Context) ([]protocol.Incident, error) {
	return s.queryIncidents(ctx,
		`SELECT id, target_id, target_name, state, started_at, resolved_at, summary
		 FROM incidents WHERE resolved_at IS NULL ORDER BY started_at ASC`)
}

func (s *sqlStore) Incidents(ctx context.Context, f IncidentFilter) ([]protocol.Incident, error) {
	q := `SELECT id, target_id, target_name, state, started_at, resolved_at, summary FROM incidents WHERE 1=1`
	var args []any

	if f.TargetID != "" {
		q += ` AND target_id = ?`
		args = append(args, f.TargetID)
	}
	if !f.Since.IsZero() {
		q += ` AND started_at >= ?`
		args = append(args, f.Since.Unix())
	}
	if !f.Until.IsZero() {
		q += ` AND started_at <= ?`
		args = append(args, f.Until.Unix())
	}
	q += ` ORDER BY started_at DESC`
	if f.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, f.Limit)
	}

	return s.queryIncidents(ctx, q, args...)
}

func (s *sqlStore) queryIncidents(ctx context.Context, q string, args ...any) ([]protocol.Incident, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: incidents: %w", err)
	}
	defer rows.Close()

	var out []protocol.Incident
	for rows.Next() {
		var in protocol.Incident
		var state string
		var startedAt int64
		var resolvedAt sql.NullInt64
		if err := rows.Scan(&in.ID, &in.TargetID, &in.TargetName, &state, &startedAt, &resolvedAt, &in.Summary); err != nil {
			return nil, fmt.Errorf("store: scan incident: %w", err)
		}
		in.State = protocol.State(state)
		in.StartedAt = time.Unix(startedAt, 0).UTC()
		if resolvedAt.Valid {
			t := time.Unix(resolvedAt.Int64, 0).UTC()
			in.ResolvedAt = &t
		}
		out = append(out, in)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: incidents: %w", err)
	}
	return out, nil
}
