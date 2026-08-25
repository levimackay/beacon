package store

import (
	"context"
	"fmt"

	"github.com/levimackay/beacon/internal/protocol"
)

func (s *sqlStore) UpsertTarget(ctx context.Context, t protocol.Target) error {
	const q = `
		INSERT INTO targets (id, kind, name, address, interval_seconds, expect_status, enabled)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			kind = excluded.kind,
			name = excluded.name,
			address = excluded.address,
			interval_seconds = excluded.interval_seconds,
			expect_status = excluded.expect_status,
			enabled = excluded.enabled
	`
	_, err := s.db.ExecContext(ctx, q,
		t.ID, string(t.Kind), t.Name, t.Address, t.IntervalSeconds, t.ExpectStatus, boolToInt(t.Enabled))
	if err != nil {
		return fmt.Errorf("store: upsert target %s: %w", t.ID, err)
	}
	return nil
}

func (s *sqlStore) DeleteTarget(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM targets WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: delete target %s: %w", id, err)
	}
	return nil
}

func (s *sqlStore) Targets(ctx context.Context) ([]protocol.Target, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kind, name, address, interval_seconds, expect_status, enabled FROM targets ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: targets: %w", err)
	}
	defer rows.Close()

	var out []protocol.Target
	for rows.Next() {
		var t protocol.Target
		var kind string
		var enabled int
		if err := rows.Scan(&t.ID, &kind, &t.Name, &t.Address, &t.IntervalSeconds, &t.ExpectStatus, &enabled); err != nil {
			return nil, fmt.Errorf("store: scan target: %w", err)
		}
		t.Kind = protocol.TargetKind(kind)
		t.Enabled = enabled != 0
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
