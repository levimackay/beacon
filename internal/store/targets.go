package store

import (
	"context"
	"fmt"

	"github.com/levimackay/beacon/internal/protocol"
)

func (s *sqlStore) UpsertTarget(ctx context.Context, t protocol.Target) error {
	const q = `
		INSERT INTO targets (id, kind, name, address, interval_seconds, expect_status, enabled, allow_private)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			kind = excluded.kind,
			name = excluded.name,
			address = excluded.address,
			interval_seconds = excluded.interval_seconds,
			expect_status = excluded.expect_status,
			allow_private = excluded.allow_private,
			enabled = excluded.enabled
	`
	_, err := s.db.ExecContext(ctx, q,
		t.ID, string(t.Kind), t.Name, t.Address, t.IntervalSeconds, t.ExpectStatus,
		boolToInt(t.Enabled), boolToInt(t.AllowPrivate))
	if err != nil {
		return fmt.Errorf("store: upsert target %s: %w", t.ID, err)
	}
	return nil
}

func (s *sqlStore) DeleteTarget(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: delete target %s: %w", id, err)
	}
	defer tx.Rollback()

	// Close any incident still open against this target. Nothing will
	// ever check it again, so an incident left open here can never
	// recover: it would sit in the open list forever, keeping the whole
	// system permanently reported as unhealthy for a target the user
	// deliberately removed. The incident history itself is kept, because
	// the outage did happen.
	if _, err := tx.ExecContext(ctx,
		`UPDATE incidents SET resolved_at = ? WHERE target_id = ? AND resolved_at IS NULL`,
		s.clock.Now().Unix(), id); err != nil {
		return fmt.Errorf("store: resolve incidents for deleted target %s: %w", id, err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM targets WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: delete target %s: %w", id, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: delete target %s: %w", id, err)
	}
	return nil
}

func (s *sqlStore) Targets(ctx context.Context) ([]protocol.Target, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kind, name, address, interval_seconds, expect_status, enabled, allow_private FROM targets ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: targets: %w", err)
	}
	defer rows.Close()

	var out []protocol.Target
	for rows.Next() {
		var t protocol.Target
		var kind string
		var enabled, allowPrivate int
		if err := rows.Scan(&t.ID, &kind, &t.Name, &t.Address, &t.IntervalSeconds, &t.ExpectStatus,
			&enabled, &allowPrivate); err != nil {
			return nil, fmt.Errorf("store: scan target: %w", err)
		}
		t.Kind = protocol.TargetKind(kind)
		t.Enabled = enabled != 0
		t.AllowPrivate = allowPrivate != 0
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: targets: %w", err)
	}
	return out, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
